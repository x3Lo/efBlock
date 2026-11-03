package cache

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// fakeClock permet de faire avancer le temps sans attendre.
type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func newTestCache(max int, maxTTL time.Duration) (*Cache, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1_000_000, 0)}
	c := New(max, maxTTL)
	c.now = clk.now
	return c, clk
}

func query(name string, qtype uint16, id uint16) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(name, qtype)
	m.Id = id
	return m
}

func answerA(req *dns.Msg, ip string, ttl uint32) *dns.Msg {
	r := new(dns.Msg)
	r.SetReply(req)
	r.Answer = []dns.RR{&dns.A{
		Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl},
		A:   net.ParseIP(ip),
	}}
	return r
}

func TestGetAfterPutDecrementsTTL(t *testing.T) {
	c, clk := newTestCache(10, 0)
	req := query("example.com.", dns.TypeA, 1)
	c.Put(req, answerA(req, "1.2.3.4", 60))

	clk.advance(25 * time.Second)
	got, ok := c.Get(query("example.com.", dns.TypeA, 777))
	if !ok {
		t.Fatal("miss inattendu")
	}
	if ttl := got.Answer[0].Header().Ttl; ttl != 35 {
		t.Errorf("TTL = %d, attendu 35 (60 - 25 s écoulées)", ttl)
	}
	if got.Id != 777 {
		t.Errorf("ID = %d, attendu celui de la nouvelle requête (777)", got.Id)
	}
}

func TestExpiry(t *testing.T) {
	c, clk := newTestCache(10, 0)
	req := query("example.com.", dns.TypeA, 1)
	c.Put(req, answerA(req, "1.2.3.4", 30))

	clk.advance(29 * time.Second)
	if _, ok := c.Get(req); !ok {
		t.Error("à 29 s l'entrée (TTL 30) devrait être valide")
	}
	clk.advance(1 * time.Second)
	if _, ok := c.Get(req); ok {
		t.Error("à 30 s l'entrée devrait être périmée")
	}
}

func TestMinTTLOfAllRecords(t *testing.T) {
	// CNAME (TTL 300) -> A (TTL 20) : la réponse n'est bonne que 20 s.
	c, clk := newTestCache(10, 0)
	req := query("www.example.com.", dns.TypeA, 1)
	resp := answerA(req, "1.2.3.4", 20)
	resp.Answer = append([]dns.RR{&dns.CNAME{
		Hdr:    dns.RR_Header{Name: "www.example.com.", Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 300},
		Target: "example.com.",
	}}, resp.Answer...)
	c.Put(req, resp)

	clk.advance(21 * time.Second)
	if _, ok := c.Get(req); ok {
		t.Error("l'entrée doit expirer avec le plus petit TTL (20 s)")
	}
}

func TestMaxTTLClamp(t *testing.T) {
	c, clk := newTestCache(10, 10*time.Second)
	req := query("example.com.", dns.TypeA, 1)
	c.Put(req, answerA(req, "1.2.3.4", 3600))

	got, ok := c.Get(req)
	if !ok || got.Answer[0].Header().Ttl != 10 {
		t.Fatalf("le TTL servi doit être plafonné à 10 s, obtenu %+v", got)
	}
	clk.advance(11 * time.Second)
	if _, ok := c.Get(req); ok {
		t.Error("l'entrée doit expirer au plafond de 10 s")
	}
}

func TestNegativeCachingUsesSOA(t *testing.T) {
	c, clk := newTestCache(10, 0)
	req := query("nexistepas.example.com.", dns.TypeA, 1)
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Rcode = dns.RcodeNameError
	resp.Ns = []dns.RR{&dns.SOA{
		Hdr: dns.RR_Header{Name: "example.com.", Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 3600},
		Ns:  "ns.example.com.", Mbox: "admin.example.com.", Minttl: 45, // min(3600, 45) = 45
	}}
	c.Put(req, resp)

	got, ok := c.Get(req)
	if !ok || got.Rcode != dns.RcodeNameError {
		t.Fatal("le NXDOMAIN aurait dû être mis en cache")
	}
	clk.advance(46 * time.Second)
	if _, ok := c.Get(req); ok {
		t.Error("le cache négatif doit expirer après min(TTL SOA, minimum SOA) = 45 s")
	}
}

func TestNotCached(t *testing.T) {
	c, _ := newTestCache(10, 0)
	req := query("example.com.", dns.TypeA, 1)

	servfail := new(dns.Msg)
	servfail.SetRcode(req, dns.RcodeServerFailure)
	c.Put(req, servfail)

	truncated := answerA(req, "1.2.3.4", 60)
	truncated.Truncated = true
	c.Put(req, truncated)

	c.Put(req, answerA(req, "1.2.3.4", 0)) // TTL 0

	nodataNoSOA := new(dns.Msg) // NOERROR sans réponse ni SOA : aucune durée connue
	nodataNoSOA.SetReply(req)
	c.Put(req, nodataNoSOA)

	if _, _, n := c.Stats(); n != 0 {
		t.Errorf("%d entrée(s) en cache, aucune attendue", n)
	}
}

func TestKeyCaseInsensitiveButQuestionEchoed(t *testing.T) {
	c, _ := newTestCache(10, 0)
	req := query("example.com.", dns.TypeA, 1)
	c.Put(req, answerA(req, "1.2.3.4", 60))

	// Certains clients mélangent les majuscules (protection "0x20") : même
	// domaine, mais la réponse doit répéter la question exactement comme envoyée.
	mixed := query("ExAmPlE.CoM.", dns.TypeA, 2)
	got, ok := c.Get(mixed)
	if !ok {
		t.Fatal("la recherche doit ignorer la casse")
	}
	if got.Question[0].Name != "ExAmPlE.CoM." {
		t.Errorf("question = %q, attendu la casse du client", got.Question[0].Name)
	}
}

func TestDifferentTypesAndDOBitAreSeparate(t *testing.T) {
	c, _ := newTestCache(10, 0)
	reqA := query("example.com.", dns.TypeA, 1)
	c.Put(reqA, answerA(reqA, "1.2.3.4", 60))

	if _, ok := c.Get(query("example.com.", dns.TypeAAAA, 1)); ok {
		t.Error("A et AAAA ne doivent pas partager d'entrée")
	}
	withDO := query("example.com.", dns.TypeA, 1)
	withDO.SetEdns0(1232, true)
	if _, ok := c.Get(withDO); ok {
		t.Error("une requête DO=1 ne doit pas recevoir une réponse mise en cache pour DO=0")
	}
}

func TestOPTRecordTTLUntouched(t *testing.T) {
	c, clk := newTestCache(10, 0)
	req := query("example.com.", dns.TypeA, 1)
	req.SetEdns0(1232, false)
	resp := answerA(req, "1.2.3.4", 60)
	resp.SetEdns0(1232, false)
	optBefore := resp.IsEdns0().Hdr.Ttl
	c.Put(req, resp)

	clk.advance(10 * time.Second)
	got, _ := c.Get(req)
	if got.IsEdns0() == nil || got.IsEdns0().Hdr.Ttl != optBefore {
		t.Error("le \"TTL\" de l'OPT contient des drapeaux EDNS : il ne doit pas être décrémenté")
	}
}

func TestLRUEviction(t *testing.T) {
	c, _ := newTestCache(2, 0)
	a, b, d := query("a.example.com.", dns.TypeA, 1), query("b.example.com.", dns.TypeA, 1), query("d.example.com.", dns.TypeA, 1)
	c.Put(a, answerA(a, "1.1.1.1", 60))
	c.Put(b, answerA(b, "2.2.2.2", 60))
	c.Get(a) // "a" devient le plus récemment utilisé : "b" est le prochain évincé
	c.Put(d, answerA(d, "3.3.3.3", 60))

	if _, ok := c.Get(b); ok {
		t.Error("b (le moins récemment utilisé) aurait dû être évincé")
	}
	if _, ok := c.Get(a); !ok {
		t.Error("a aurait dû rester")
	}
	if _, ok := c.Get(d); !ok {
		t.Error("d aurait dû être présent")
	}
}

// Modifier la réponse reçue ne doit jamais corrompre ce qui est stocké.
func TestGetReturnsIndependentCopy(t *testing.T) {
	c, _ := newTestCache(10, 0)
	req := query("example.com.", dns.TypeA, 1)
	c.Put(req, answerA(req, "1.2.3.4", 60))

	first, _ := c.Get(req)
	first.Answer[0].(*dns.A).A = net.ParseIP("9.9.9.9")

	second, _ := c.Get(req)
	if ip := second.Answer[0].(*dns.A).A.String(); ip != "1.2.3.4" {
		t.Errorf("le cache a été corrompu: %s", ip)
	}
}
