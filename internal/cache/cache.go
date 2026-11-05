// Package cache garde en mémoire les réponses DNS de l'upstream, pour répondre
// instantanément (0 ms) aux requêtes répétées, en respectant les TTL.
package cache

import (
	"container/list"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

// key identifie une question. Deux requêtes ont la même réponse si elles ont
// même nom, même type (A, AAAA...), même classe, et même bit DO.
//
// Concept DNS : le bit "DO" (DNSSEC OK) demande à l'upstream d'ajouter les
// signatures DNSSEC dans sa réponse. La réponse n'est donc pas la même selon
// ce bit : il fait partie de la clé, sinon on servirait une réponse sans
// signatures à un client qui en demande.
type key struct {
	name   string // en minuscules : "Example.COM." et "example.com." sont le même nom
	qtype  uint16
	qclass uint16
	do     bool
}

func keyFor(req *dns.Msg) key {
	q := req.Question[0]
	k := key{name: strings.ToLower(q.Name), qtype: q.Qtype, qclass: q.Qclass}
	if opt := req.IsEdns0(); opt != nil {
		k.do = opt.Do()
	}
	return k
}

// KeyString identifie une question sous forme de texte (pour fusionner les requêtes identiques).
func KeyString(req *dns.Msg) string {
	if len(req.Question) != 1 {
		return ""
	}
	k := keyFor(req)
	return fmt.Sprintf("%s|%d|%d|%t", k.name, k.qtype, k.qclass, k.do)
}

// entry est une réponse mémorisée. Une fois stockée, elle n'est JAMAIS
// modifiée : chaque lecture travaille sur une copie.
type entry struct {
	key         key
	msg         *dns.Msg
	stored      time.Time
	expires     time.Time
	ttl         time.Duration // durée de vie d'origine (sert à décider du préchargement)
	hits        uint32        // nombre de fois servie (sert à décider du préchargement)
	prefetching bool          // un rafraîchissement est déjà en cours
}

// Cache est un cache DNS borné (éviction LRU), sûr pour un usage concurrent.
type Cache struct {
	mu     sync.Mutex
	max    int
	maxTTL time.Duration

	staleMax time.Duration // durée pendant laquelle une réponse périmée reste utilisable en secours (0 = jamais)
	prefetch bool          // rafraîchir en avance les réponses populaires

	items map[key]*list.Element // clé -> noeud de la liste
	lru   *list.List            // devant = utilisé récemment, fond = candidat à l'éviction
	now   func() time.Time      // horloge, remplaçable dans les tests

	hits, misses, stale atomic.Uint64
}

// staleTTL : durée (secondes) annoncée aux clients pour une réponse périmée.
// 30 s est la valeur recommandée par la RFC 8767 : assez pour que le client ne
// redemande pas en boucle, assez court pour qu'il retente vite quand tout va mieux.
const staleTTL = 30

// Seuils du préchargement : une réponse déjà demandée au moins 2 fois dont il
// reste moins de 10 % de la durée de vie est rafraîchie en arrière-plan, pour
// qu'elle ne soit jamais périmée au moment où on en a besoin.
const (
	prefetchMinHits  = 2
	prefetchFraction = 10
	prefetchMinTTL   = 10 * time.Second
)

// SetStale règle la durée pendant laquelle une réponse périmée peut servir de secours
// quand aucun upstream ne répond (0 = désactivé).
func (c *Cache) SetStale(d time.Duration) {
	c.mu.Lock()
	c.staleMax = d
	c.mu.Unlock()
}

// SetPrefetch active ou désactive le préchargement des réponses populaires.
func (c *Cache) SetPrefetch(on bool) {
	c.mu.Lock()
	c.prefetch = on
	c.mu.Unlock()
}

// New crée un cache de maxEntries réponses au plus. maxTTL plafonne la durée
// de vie d'une entrée (0 = pas de plafond).
func New(maxEntries int, maxTTL time.Duration) *Cache {
	return &Cache{
		max:    maxEntries,
		maxTTL: maxTTL,
		items:  make(map[key]*list.Element),
		lru:    list.New(),
		now:    time.Now,
	}
}

// Stats retourne le nombre de réponses servies depuis le cache, de ratés, et d'entrées.
func (c *Cache) Stats() (hits, misses uint64, entries int) {
	c.mu.Lock()
	entries = c.lru.Len()
	c.mu.Unlock()
	return c.hits.Load(), c.misses.Load(), entries
}

// StaleServed retourne le nombre de réponses périmées servies en secours.
func (c *Cache) StaleServed() uint64 { return c.stale.Load() }

// Result est le résultat d'une recherche dans le cache.
type Result struct {
	Msg      *dns.Msg // la réponse prête à être envoyée ; nil si absente ou périmée
	Prefetch bool     // true : la réponse est valide mais bientôt périmée, à rafraîchir en arrière-plan
}

// Get cherche la réponse à req. Si elle est en cache et encore valide, elle est
// retournée prête à être envoyée : bon ID, bonne question, TTL décrémentés.
func (c *Cache) Get(req *dns.Msg) (*dns.Msg, bool) {
	r := c.Lookup(req)
	return r.Msg, r.Msg != nil
}

// Lookup est comme Get, et indique en plus s'il faut précharger cette réponse.
func (c *Cache) Lookup(req *dns.Msg) Result {
	if len(req.Question) != 1 {
		return Result{}
	}
	k := keyFor(req)

	c.mu.Lock()
	el, ok := c.items[k]
	if !ok {
		c.mu.Unlock()
		c.misses.Add(1)
		return Result{}
	}
	e := el.Value.(*entry)
	now := c.now()
	if !now.Before(e.expires) { // entrée périmée
		// On la garde un moment : elle peut encore servir de secours (voir GetStale).
		if c.staleMax <= 0 || !now.Before(e.expires.Add(c.staleMax)) {
			c.lru.Remove(el)
			delete(c.items, k)
		}
		c.mu.Unlock()
		c.misses.Add(1)
		return Result{}
	}
	c.lru.MoveToFront(el)
	e.hits++
	remaining := e.expires.Sub(now)
	prefetch := c.prefetch && !e.prefetching && e.hits >= prefetchMinHits &&
		e.ttl >= prefetchMinTTL && remaining < e.ttl/prefetchFraction
	if prefetch {
		e.prefetching = true // un seul rafraîchissement à la fois pour cette réponse
	}
	c.mu.Unlock()
	c.hits.Add(1)

	// e.msg n'est jamais modifié après Put : on peut le copier hors du verrou.
	msg := e.msg.Copy()
	elapsed := uint32(now.Sub(e.stored) / time.Second)
	remainingSec := uint32((remaining + time.Second - 1) / time.Second) // arrondi au-dessus
	adjustTTLs(msg, elapsed, remainingSec)
	finish(msg, req)
	return Result{Msg: msg, Prefetch: prefetch}
}

// GetStale retourne une réponse PÉRIMÉE (mais pas trop ancienne) pour req, à utiliser
// quand aucun upstream ne répond.
//
// Concept DNS (RFC 8767, "serve-stale") : une réponse un peu vieille vaut mieux que
// pas de réponse du tout. Si tous les upstreams sont injoignables, un site déjà
// visité continue de s'ouvrir avec son ancienne adresse IP, au lieu de tomber en
// erreur. Les adresses changent rarement, et le client est invité à réessayer
// bientôt (TTL de 30 s).
func (c *Cache) GetStale(req *dns.Msg) (*dns.Msg, bool) {
	if len(req.Question) != 1 {
		return nil, false
	}
	k := keyFor(req)
	c.mu.Lock()
	el, ok := c.items[k]
	if !ok || c.staleMax <= 0 {
		c.mu.Unlock()
		return nil, false
	}
	e := el.Value.(*entry)
	if !c.now().Before(e.expires.Add(c.staleMax)) { // vraiment trop vieille
		c.lru.Remove(el)
		delete(c.items, k)
		c.mu.Unlock()
		return nil, false
	}
	c.mu.Unlock()
	c.stale.Add(1)

	msg := e.msg.Copy()
	adjustTTLs(msg, 0, staleTTL)
	for _, section := range [][]dns.RR{msg.Answer, msg.Ns, msg.Extra} {
		for _, rr := range section {
			if h := rr.Header(); h.Rrtype != dns.TypeOPT {
				h.Ttl = staleTTL
			}
		}
	}
	finish(msg, req)
	return msg, true
}

// finish fait de msg la réponse à CETTE requête : même ID (le client s'en sert pour
// associer question et réponse) et même question, avec la casse exacte envoyée.
func finish(msg, req *dns.Msg) {
	msg.Id = req.Id
	msg.Question = []dns.Question{req.Question[0]}
	msg.RecursionDesired = req.RecursionDesired
}

// Put mémorise resp (la réponse de l'upstream à req) si elle est cachable.
func (c *Cache) Put(req, resp *dns.Msg) {
	if len(req.Question) != 1 || resp == nil || resp.Truncated {
		return // une réponse tronquée est incomplète : ne jamais la garder
	}
	// On ne cache que les réponses "définitives" : NOERROR (y compris NODATA)
	// et NXDOMAIN. Un SERVFAIL est une panne passagère : on veut réessayer.
	if resp.Rcode != dns.RcodeSuccess && resp.Rcode != dns.RcodeNameError {
		return
	}
	ttl := cacheTTL(resp)
	if ttl == 0 {
		return // TTL 0 = "ne me garde pas", ou rien pour calculer une durée
	}
	d := time.Duration(ttl) * time.Second
	if c.maxTTL > 0 && d > c.maxTTL {
		d = c.maxTTL
	}

	now := c.now()
	e := &entry{key: keyFor(req), msg: resp.Copy(), stored: now, expires: now.Add(d), ttl: d}

	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[e.key]; ok { // remplace une entrée existante
		el.Value = e
		c.lru.MoveToFront(el)
		return
	}
	c.items[e.key] = c.lru.PushFront(e)
	for c.lru.Len() > c.max { // plein : on évince le moins récemment utilisé
		oldest := c.lru.Back()
		c.lru.Remove(oldest)
		delete(c.items, oldest.Value.(*entry).key)
	}
}

// cacheTTL détermine combien de secondes on peut garder resp. 0 = ne pas cacher.
//
// Concept DNS : chaque enregistrement (RR) porte un TTL, sa durée de validité
// en secondes. Une réponse contient plusieurs RR (ex: une chaîne CNAME puis une
// adresse A) : elle n'est valable que tant que TOUS le sont, donc on prend le
// plus petit TTL.
//
// Cache négatif (RFC 2308) : pour "ce domaine n'existe pas" (NXDOMAIN) ou "il
// n'a pas ce type d'enregistrement" (NODATA), la réponse n'a aucun RR de
// réponse, donc pas de TTL. C'est l'enregistrement SOA de la section
// "authority" qui donne la durée : le plus petit entre son TTL et son champ
// "minimum". Sans cache négatif, un domaine inexistant (typo, malware, pub
// bloquée en amont...) serait redemandé à l'upstream à chaque fois.
func cacheTTL(resp *dns.Msg) uint32 {
	if len(resp.Answer) == 0 {
		for _, rr := range resp.Ns {
			if soa, ok := rr.(*dns.SOA); ok {
				return min(soa.Hdr.Ttl, soa.Minttl)
			}
		}
		return 0
	}
	ttl, found := uint32(0), false
	for _, section := range [][]dns.RR{resp.Answer, resp.Ns, resp.Extra} {
		for _, rr := range section {
			h := rr.Header()
			if h.Rrtype == dns.TypeOPT {
				continue // le champ "TTL" d'un OPT contient des drapeaux EDNS, pas une durée
			}
			if !found || h.Ttl < ttl {
				ttl, found = h.Ttl, true
			}
		}
	}
	return ttl
}

// adjustTTLs fait "vieillir" les enregistrements : on retranche le temps passé
// en cache, sans jamais dépasser la durée de vie restante de l'entrée.
// Un client qui reçoit TTL=20 sait ainsi qu'il peut garder la réponse 20 s de plus.
func adjustTTLs(msg *dns.Msg, elapsed, remaining uint32) {
	for _, section := range [][]dns.RR{msg.Answer, msg.Ns, msg.Extra} {
		for _, rr := range section {
			h := rr.Header()
			if h.Rrtype == dns.TypeOPT {
				continue
			}
			var ttl uint32
			if h.Ttl > elapsed {
				ttl = h.Ttl - elapsed
			}
			h.Ttl = min(ttl, remaining)
		}
	}
}
