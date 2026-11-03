// Package localdns gère les noms du réseau local : noms fixes (/etc/hosts), domaines
// confiés au résolveur de la maison (ta box), et zones inverses privées.
//
// Sans lui, une requête comme "nas.maison" ou la recherche inverse d'une IP privée
// (192.168.1.20 -> nom) partirait vers internet : elle échouerait, et ferait fuiter
// la structure de ton réseau chez le fournisseur de DNS.
package localdns

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/x3Lo/efBlock/internal/upstream"
)

// localTTL : durée de validité des réponses locales (courte : un nom peut changer).
const localTTL = 60

// specialDomains ne doivent jamais partir sur internet (RFC 6761, RFC 8375).
var specialDomains = []string{"localhost", "local", "home.arpa", "invalid"}

// privateReverse : zones inverses des adresses privées (RFC 6303). Les interroger
// sur internet n'a aucun sens : seul ton réseau sait à quoi correspondent ces adresses.
var privateReverse = func() []string {
	z := []string{
		"10.in-addr.arpa", "168.192.in-addr.arpa", "254.169.in-addr.arpa", "127.in-addr.arpa", "0.in-addr.arpa",
		"d.f.ip6.arpa", "c.f.ip6.arpa", // fc00::/7
		"8.e.f.ip6.arpa", "9.e.f.ip6.arpa", "a.e.f.ip6.arpa", "b.e.f.ip6.arpa", // fe80::/10
		"1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.ip6.arpa", // ::1
	}
	for i := 16; i <= 31; i++ { // 172.16.0.0/12
		z = append(z, fmt.Sprintf("%d.172.in-addr.arpa", i))
	}
	return z
}()

// Zone décide du sort des noms locaux.
type Zone struct {
	domains  []string            // suffixes confiés au résolveur local (config + spéciaux)
	resolver *upstream.Forwarder // le résolveur local (ta box) ; nil = aucun
	hosts    map[string][]net.IP // nom -> adresses
	ptr      map[string]string   // nom inverse -> nom
	names    *nameCache
}

// New construit une zone.
//   - upstreamAddr : résolveur local ("192.168.1.1" ou "192.168.1.1:53"), vide = aucun.
//   - domains      : suffixes à envoyer à ce résolveur ("lan", "maison").
//   - hosts        : lignes "IP nom [nom...]" comme dans /etc/hosts.
func New(upstreamAddr string, domains, hosts []string, timeout time.Duration) (*Zone, error) {
	z := &Zone{hosts: map[string][]net.IP{}, ptr: map[string]string{}}

	for _, d := range append(append([]string(nil), domains...), specialDomains...) {
		d = strings.Trim(strings.ToLower(strings.TrimSpace(d)), ".")
		if d == "" {
			continue
		}
		if !validName(d) {
			return nil, fmt.Errorf("domaine local %q invalide", d)
		}
		z.domains = append(z.domains, d)
	}

	if strings.TrimSpace(upstreamAddr) != "" {
		f, err := upstream.New([]string{strings.TrimSpace(upstreamAddr)}, timeout, nil)
		if err != nil {
			return nil, fmt.Errorf("résolveur local: %w", err)
		}
		z.resolver = f
	} else if len(domains) > 0 {
		return nil, errors.New("des domaines locaux sont définis sans résolveur local")
	}

	for _, line := range hosts {
		line = strings.TrimSpace(line)
		if i := strings.IndexByte(line, '#'); i >= 0 { // commentaire
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		ip := net.ParseIP(fields[0])
		if ip == nil || len(fields) < 2 {
			return nil, fmt.Errorf("hôte %q : attendu \"IP nom [nom...]\" (ex: 192.168.1.20 nas.maison)", line)
		}
		for _, n := range fields[1:] {
			n = strings.Trim(strings.ToLower(n), ".")
			if !validName(n) {
				return nil, fmt.Errorf("hôte %q : nom %q invalide", line, n)
			}
			z.hosts[n] = append(z.hosts[n], ip)
			if rev, err := dns.ReverseAddr(ip.String()); err == nil {
				rev = strings.TrimSuffix(strings.ToLower(rev), ".")
				if _, taken := z.ptr[rev]; !taken { // le premier nom d'une IP est son nom "officiel"
					z.ptr[rev] = n
				}
			}
		}
	}
	z.names = newNameCache(z)
	return z, nil
}

// validName accepte un nom de machine ou de domaine (une seule étiquette suffit : "nas").
func validName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, l := range strings.Split(s, ".") {
		if l == "" || len(l) > 63 {
			return false
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return false
			}
		}
	}
	return true
}

func hasSuffix(name, suffix string) bool {
	return name == suffix || strings.HasSuffix(name, "."+suffix)
}

// Answer traite req si elle concerne le réseau local. handled=false : ce n'est pas
// une requête locale, le traitement normal (filtre, cache, internet) continue.
func (z *Zone) Answer(req *dns.Msg) (resp *dns.Msg, handled bool) {
	if z == nil || len(req.Question) != 1 {
		return nil, false
	}
	q := req.Question[0]
	name := strings.TrimSuffix(strings.ToLower(q.Name), ".")

	// 1. Noms fixes.
	if ips, ok := z.hosts[name]; ok {
		resp = reply(req)
		resp.Authoritative = true
		for _, ip := range ips {
			hdr := dns.RR_Header{Name: q.Name, Class: dns.ClassINET, Ttl: localTTL}
			if v4 := ip.To4(); v4 != nil && q.Qtype == dns.TypeA {
				hdr.Rrtype = dns.TypeA
				resp.Answer = append(resp.Answer, &dns.A{Hdr: hdr, A: v4})
			} else if v4 == nil && q.Qtype == dns.TypeAAAA {
				hdr.Rrtype = dns.TypeAAAA
				resp.Answer = append(resp.Answer, &dns.AAAA{Hdr: hdr, AAAA: ip})
			}
		}
		return resp, true // aucune adresse de ce type : réponse vide NOERROR ("NODATA")
	}

	// 2. Recherche inverse d'un nom fixe.
	if q.Qtype == dns.TypePTR {
		if target, ok := z.ptr[name]; ok {
			resp = reply(req)
			resp.Authoritative = true
			resp.Answer = []dns.RR{&dns.PTR{
				Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: localTTL},
				Ptr: dns.Fqdn(target),
			}}
			return resp, true
		}
	}

	// 3. Domaine local ou zone inverse privée : jamais envoyé sur internet.
	if z.isLocal(name) {
		if z.resolver != nil {
			r, err := z.resolver.Resolve(req)
			if err != nil {
				resp = new(dns.Msg)
				resp.SetRcode(req, dns.RcodeServerFailure)
				return resp, true
			}
			return r, true
		}
		resp = reply(req) // personne ne connaît ce nom ici : il n'existe pas
		resp.Rcode = dns.RcodeNameError
		return resp, true
	}
	return nil, false
}

func (z *Zone) isLocal(name string) bool {
	for _, d := range z.domains {
		if hasSuffix(name, d) {
			return true
		}
	}
	for _, d := range privateReverse {
		if hasSuffix(name, d) {
			return true
		}
	}
	return false
}

func reply(req *dns.Msg) *dns.Msg {
	r := new(dns.Msg)
	r.SetReply(req)
	r.RecursionAvailable = true
	return r
}

// ClientName retourne le nom d'une machine du réseau à partir de son IP, ou "".
// Ne bloque jamais : si le nom n'est pas encore connu, une recherche part en
// arrière-plan et le résultat sera disponible à l'appel suivant.
func (z *Zone) ClientName(ip string) string {
	if z == nil {
		return ""
	}
	return z.names.get(ip)
}

// --- Noms des clients ---------------------------------------------------------

type nameEntry struct {
	name string
	at   time.Time
}

type nameCache struct {
	z        *Zone
	mu       sync.Mutex
	m        map[string]nameEntry
	inflight map[string]bool
}

const (
	nameTTL    = 10 * time.Minute
	nameNegTTL = 2 * time.Minute
	nameMax    = 2048
)

func newNameCache(z *Zone) *nameCache {
	return &nameCache{z: z, m: map[string]nameEntry{}, inflight: map[string]bool{}}
}

func (c *nameCache) get(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	rev, err := dns.ReverseAddr(parsed.String())
	if err != nil {
		return ""
	}
	rev = strings.TrimSuffix(strings.ToLower(rev), ".")
	if n, ok := c.z.ptr[rev]; ok { // nom fixe configuré : réponse immédiate
		return n
	}
	if c.z.resolver == nil {
		return ""
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[ip]; ok {
		ttl := nameTTL
		if e.name == "" {
			ttl = nameNegTTL
		}
		if time.Since(e.at) < ttl {
			return e.name
		}
	}
	if !c.inflight[ip] {
		c.inflight[ip] = true
		go c.lookup(ip, rev)
	}
	return c.m[ip].name // périmé ou vide, en attendant le résultat frais
}

func (c *nameCache) lookup(ip, rev string) {
	name := ""
	req := new(dns.Msg).SetQuestion(dns.Fqdn(rev), dns.TypePTR)
	if resp, err := c.z.resolver.Resolve(req); err == nil {
		for _, rr := range resp.Answer {
			if p, ok := rr.(*dns.PTR); ok {
				name = strings.TrimSuffix(strings.ToLower(p.Ptr), ".")
				break
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, ip)
	if len(c.m) >= nameMax {
		c.m = map[string]nameEntry{} // plafond atteint : on repart de zéro
	}
	c.m[ip] = nameEntry{name: name, at: time.Now()}
}
