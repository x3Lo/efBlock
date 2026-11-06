package filter

import (
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"time"
)

// Reason explique une décision de filtrage (utile pour les logs et le dashboard).
type Reason int

const (
	ReasonNone      Reason = iota // domaine inconnu des règles : on laisse passer
	ReasonUserAllow               // allowlist perso (globale ou du groupe)
	ReasonUserBlock               // blocklist perso (globale ou du groupe)
	ReasonListAllow               // exception (@@) d'une liste de filtres
	ReasonList                    // domaine présent dans une liste de filtres
	ReasonGroupOff                // le groupe du client n'a aucun blocage
)

func (r Reason) String() string {
	return [...]string{"none", "allowlist", "blocklist", "list-exception", "list", "group-off"}[r]
}

// Verdict est le résultat d'une vérification.
type Verdict struct {
	Blocked bool
	Reason  Reason
	Rule    string // la règle qui a matché (ex: "doubleclick.net")
	Group   string // nom du groupe de clients appliqué ("" = réglages par défaut)
	Via     string // si bloqué à cause d'un alias (CNAME) : le nom vers lequel le domaine pointe
}

// listSets : les domaines d'UNE liste de filtres. Chaque liste garde son propre
// ensemble, pour qu'un groupe de clients puisse n'en utiliser que certaines.
type listSets struct {
	id    string // identifiant de la liste (son adresse)
	block Set
	allow Set
}

// listIndex est l'index construit à partir des listes téléchargées.
// Il est immuable une fois publié : on ne le modifie jamais, on le remplace.
type listIndex struct {
	lists []listSets
}

// userRules : allowlist et blocklist perso de l'utilisateur.
type userRules struct {
	allow Set
	block Set
}

// Group est un ensemble de clients qui ont leurs propres règles de filtrage.
type Group struct {
	Name  string
	nets  []*net.IPNet
	off   bool            // true : aucun blocage pour ces clients
	lists map[string]bool // listes utilisées ; nil = toutes
	allow Set
	block Set
}

// GroupSpec décrit un groupe tel que l'utilisateur le saisit.
type GroupSpec struct {
	Name     string
	Clients  []string // adresses ("192.168.1.50") ou réseaux ("192.168.1.0/24")
	Off      bool     // aucun blocage pour ce groupe
	AllLists bool     // true : toutes les listes de filtres ; sinon, seulement Lists
	Lists    []string // adresses des listes utilisées (si AllLists est faux)
	Allow    []string // domaines jamais bloqués pour ce groupe
	Block    []string // domaines toujours bloqués pour ce groupe
}

type groupSet struct{ groups []*Group }

// ParseClient analyse une adresse ou un réseau ("192.168.1.50", "192.168.1.0/24", "fd00::/64").
// Une adresse seule devient un réseau d'une seule machine.
func ParseClient(s string) (*net.IPNet, error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "/") {
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, fmt.Errorf("%q n'est pas une adresse IP ni un réseau (ex: 192.168.1.50 ou 192.168.1.0/24)", s)
		}
		if v4 := ip.To4(); v4 != nil {
			return &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)}, nil
		}
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}, nil
	}
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		return nil, fmt.Errorf("%q n'est pas un réseau valide (ex: 192.168.1.0/24)", s)
	}
	return n, nil
}

// Engine décide si un domaine doit être bloqué. Il est sûr à utiliser depuis
// plusieurs goroutines : les requêtes DNS lisent pendant qu'une mise à jour
// remplace les données.
//
// Astuce : plutôt qu'un mutex, on garde un pointeur atomique vers des données
// immuables. Une mise à jour construit un nouvel index à côté puis échange le
// pointeur en une instruction : les lecteurs ne sont jamais bloqués et ne
// voient jamais un index à moitié construit.
type Engine struct {
	lists  atomic.Pointer[listIndex]
	user   atomic.Pointer[userRules]
	groups atomic.Pointer[groupSet]

	enabled    atomic.Bool
	cnameCheck atomic.Bool // vérifier aussi les alias (CNAME) des réponses

	// pausedUntil : fin d'une pause temporaire (nanosecondes Unix, 0 = pas de pause).
	pausedUntil atomic.Int64
}

// NewEngine crée un moteur actif, sans aucune règle.
func NewEngine() *Engine {
	e := &Engine{}
	e.lists.Store(&listIndex{})
	e.user.Store(&userRules{allow: Set{}, block: Set{}})
	e.groups.Store(&groupSet{})
	e.enabled.Store(true)
	e.cnameCheck.Store(true)
	return e
}

// SetEnabled active ou désactive globalement le blocage (sans limite de durée).
func (e *Engine) SetEnabled(on bool) { e.enabled.Store(on) }

// SetCNAMECheck active ou désactive la vérification des alias (voir CheckAliases).
func (e *Engine) SetCNAMECheck(on bool) { e.cnameCheck.Store(on) }

// Pause désactive le blocage pendant d, puis il reprend tout seul.
// Pratique quand un site légitime est cassé par un blocage : "pause 5 min".
func (e *Engine) Pause(d time.Duration) { e.pausedUntil.Store(time.Now().Add(d).UnixNano()) }

// Resume annule une pause et réactive le blocage.
func (e *Engine) Resume() {
	e.pausedUntil.Store(0)
	e.enabled.Store(true)
}

// State retourne l'interrupteur général et la fin de la pause en cours
// (zéro si aucune pause n'est en cours).
func (e *Engine) State() (enabled bool, pausedUntil time.Time) {
	enabled = e.enabled.Load()
	if n := e.pausedUntil.Load(); n != 0 {
		if t := time.Unix(0, n); t.After(time.Now()) {
			pausedUntil = t
		}
	}
	return
}

// Enabled indique si le blocage est réellement actif à cet instant.
func (e *Engine) Enabled() bool {
	enabled, until := e.State()
	return enabled && until.IsZero()
}

// ValidateDomain normalise et valide un domaine saisi par l'utilisateur.
func ValidateDomain(s string) (string, bool) { return normalizeDomain(s) }

// DomainFromInput accepte ce qu'on colle naturellement dans un champ « domaine » et en garde le nom :
// une adresse complète (https://www.exemple.com/page?x=1), un nom suivi d'un chemin
// (exemple.com/pub), d'un port (exemple.com:443), ou précédé de « *. ». Retourne le domaine validé.
func DomainFromInput(in string) (string, bool) {
	s := strings.TrimSpace(in)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 { // identifiants dans l'adresse (utilisateur@hôte)
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 && strings.Count(s, ":") == 1 { // port, jamais une IPv6
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "*.")
	return normalizeDomain(s)
}

// SetUserRules remplace l'allowlist et la blocklist perso.
// Les entrées invalides sont ignorées ; le nombre d'entrées rejetées est retourné.
func (e *Engine) SetUserRules(allow, block []string) (rejected int) {
	a, ra := domainSet(allow)
	b, rb := domainSet(block)
	e.user.Store(&userRules{allow: a, block: b})
	return ra + rb
}

func domainSet(domains []string) (s Set, rejected int) {
	s = Set{}
	for _, d := range domains {
		if n, ok := normalizeDomain(d); ok {
			s.Add(n)
		} else {
			rejected++
		}
	}
	return s, rejected
}

// SetGroups remplace les groupes de clients. En cas d'entrée invalide, rien n'est changé.
// Un client appartient au PREMIER groupe dont un réseau le contient : l'ordre compte.
func (e *Engine) SetGroups(specs []GroupSpec) error {
	set := &groupSet{}
	for _, sp := range specs {
		g := &Group{Name: sp.Name, off: sp.Off}
		for _, c := range sp.Clients {
			n, err := ParseClient(c)
			if err != nil {
				return fmt.Errorf("groupe %q : %w", sp.Name, err)
			}
			g.nets = append(g.nets, n)
		}
		if !sp.AllLists {
			g.lists = map[string]bool{}
			for _, id := range sp.Lists {
				g.lists[id] = true
			}
		}
		var ra, rb int
		g.allow, ra = domainSet(sp.Allow)
		g.block, rb = domainSet(sp.Block)
		if ra+rb > 0 {
			return fmt.Errorf("groupe %q : %d domaine(s) invalide(s) dans ses règles", sp.Name, ra+rb)
		}
		set.groups = append(set.groups, g)
	}
	e.groups.Store(set)
	return nil
}

// setLists publie un nouvel index de listes.
func (e *Engine) setLists(idx *listIndex) { e.lists.Store(idx) }

// Counts retourne le nombre de domaines bloqués et d'exceptions chargés depuis les listes
// (somme de toutes les listes : un domaine présent dans deux listes compte deux fois).
func (e *Engine) Counts() (blocked, allowed int) {
	for _, l := range e.lists.Load().lists {
		blocked += len(l.block)
		allowed += len(l.allow)
	}
	return
}

// groupFor retourne le groupe d'un client, ou nil (réglages par défaut).
func (e *Engine) groupFor(ip net.IP) *Group {
	if ip == nil {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, g := range e.groups.Load().groups {
		for _, n := range g.nets {
			if n.Contains(ip) {
				return g
			}
		}
	}
	return nil
}

// Check décide du sort de name pour un client sans groupe (réglages par défaut).
func (e *Engine) Check(name string) Verdict { return e.CheckClient(nil, name) }

// CheckClient décide du sort de name (un nom de requête DNS, avec ou sans point final)
// pour le client ip, selon son groupe.
//
// Priorité, de la plus forte à la plus faible :
//  1. groupe sans blocage     -> jamais bloqué
//  2. allowlist du groupe     -> jamais bloqué
//  3. blocklist du groupe     -> bloqué
//  4. allowlist perso         -> jamais bloqué
//  5. blocklist perso         -> bloqué
//  6. exceptions des listes   -> jamais bloqué
//  7. listes de filtres       -> bloqué (seulement celles que le groupe utilise)
func (e *Engine) CheckClient(ip net.IP, name string) Verdict {
	if !e.Enabled() {
		return Verdict{}
	}
	name = NormalizeName(name)

	g := e.groupFor(ip)
	var gname string
	if g != nil {
		gname = g.Name
		if g.off {
			return Verdict{Reason: ReasonGroupOff, Group: gname}
		}
		if rule, ok := g.allow.Match(name); ok {
			return Verdict{Reason: ReasonUserAllow, Rule: rule, Group: gname}
		}
		if rule, ok := g.block.Match(name); ok {
			return Verdict{Blocked: true, Reason: ReasonUserBlock, Rule: rule, Group: gname}
		}
	}

	u := e.user.Load()
	if rule, ok := u.allow.Match(name); ok {
		return Verdict{Reason: ReasonUserAllow, Rule: rule, Group: gname}
	}
	if rule, ok := u.block.Match(name); ok {
		return Verdict{Blocked: true, Reason: ReasonUserBlock, Rule: rule, Group: gname}
	}

	used := func(l *listSets) bool { return g == nil || g.lists == nil || g.lists[l.id] }
	lists := e.lists.Load().lists
	for i := range lists {
		if used(&lists[i]) {
			if rule, ok := lists[i].allow.Match(name); ok {
				return Verdict{Reason: ReasonListAllow, Rule: rule, Group: gname}
			}
		}
	}
	for i := range lists {
		if used(&lists[i]) {
			if rule, ok := lists[i].block.Match(name); ok {
				return Verdict{Blocked: true, Reason: ReasonList, Rule: rule, Group: gname}
			}
		}
	}
	return Verdict{Group: gname}
}

// CheckAliases vérifie les noms vers lesquels une réponse redirige (alias CNAME).
//
// Concept DNS : un domaine peut n'être qu'un alias vers un autre. Les traqueurs
// en abusent ("CNAME cloaking") : un site fait pointer metrics.monsite.fr vers
// collecte.traqueur.net. Le nom demandé semble innocent, donc le filtre le laisse
// passer, alors que la vraie destination est un traqueur. On vérifie donc aussi
// chaque cible de la chaîne d'alias.
//
// orig est le verdict déjà rendu pour le nom demandé : s'il est explicitement
// autorisé (par toi, ou par une exception de liste), on n'y touche pas.
// Retourne le verdict de la première cible bloquée, avec Via renseigné.
func (e *Engine) CheckAliases(ip net.IP, orig Verdict, targets []string) (Verdict, bool) {
	if !e.cnameCheck.Load() || !e.Enabled() || len(targets) == 0 {
		return Verdict{}, false
	}
	if orig.Reason == ReasonUserAllow || orig.Reason == ReasonListAllow || orig.Reason == ReasonGroupOff {
		return Verdict{}, false
	}
	for _, t := range targets {
		if v := e.CheckClient(ip, t); v.Blocked {
			v.Via = NormalizeName(t)
			return v, true
		}
	}
	return Verdict{}, false
}
