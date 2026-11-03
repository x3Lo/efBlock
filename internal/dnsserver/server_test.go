package dnsserver

import (
	"net"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/x3Lo/efBlock/internal/cache"
	"github.com/x3Lo/efBlock/internal/filter"
	"github.com/x3Lo/efBlock/internal/querylog"
	"github.com/x3Lo/efBlock/internal/upstream"
)

// freeAddr trouve un port libre (UDP et TCP) sur 127.0.0.1.
func freeAddr(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	return "127.0.0.1:" + strconv.Itoa(pc.LocalAddr().(*net.UDPAddr).Port)
}

// startFakeUpstream lance un faux résolveur qui répond 1.2.3.4 (TTL 60) à tout
// et compte les requêtes reçues.
func startFakeUpstream(t *testing.T) (addr string, hits *atomic.Int32) {
	t.Helper()
	addr = freeAddr(t)
	hits = new(atomic.Int32)
	started := make(chan struct{})
	srv := &dns.Server{Addr: addr, Net: "udp", NotifyStartedFunc: func() { close(started) },
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			hits.Add(1)
			resp := new(dns.Msg)
			resp.SetReply(req)
			resp.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP("1.2.3.4"),
			}}
			_ = w.WriteMsg(resp)
		})}
	go srv.ListenAndServe()
	<-started
	t.Cleanup(func() { srv.Shutdown() })
	return addr, hits
}

func startEfBlock(t *testing.T, upstreamAddr string, eng *filter.Engine, c *cache.Cache) string {
	return startEfBlockLog(t, upstreamAddr, eng, c, nil)
}

func startEfBlockLog(t *testing.T, upstreamAddr string, eng *filter.Engine, c *cache.Cache, l *querylog.Logger) string {
	t.Helper()
	addr := freeAddr(t)
	fwd, err := upstream.New([]string{upstreamAddr}, 2*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Options{
		Addr:     addr,
		Upstream: fwd,
		Filter:   eng,
		Cache:    c,
		Log:      l,
	})
	go srv.Start()
	t.Cleanup(srv.Shutdown)

	// Attend que le serveur réponde (il démarre dans une goroutine).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m := new(dns.Msg).SetQuestion("ready.test.", dns.TypeA)
		if _, err := dns.Exchange(m, addr); err == nil {
			return addr
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("le serveur n'a pas démarré")
	return ""
}

func ask(t *testing.T, addr, name string, qtype uint16) *dns.Msg {
	t.Helper()
	m := new(dns.Msg).SetQuestion(name, qtype)
	resp, err := dns.Exchange(m, addr)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestCacheAvoidsSecondUpstreamQuery(t *testing.T) {
	upAddr, hits := startFakeUpstream(t)
	addr := startEfBlock(t, upAddr, filter.NewEngine(), cache.New(100, 0))
	hits.Store(0) // on ignore la requête "ready" de startEfBlock

	first := ask(t, addr, "example.com.", dns.TypeA)
	second := ask(t, addr, "example.com.", dns.TypeA)

	if n := hits.Load(); n != 1 {
		t.Errorf("l'upstream a reçu %d requêtes, attendu 1 (la 2e vient du cache)", n)
	}
	for i, r := range []*dns.Msg{first, second} {
		if len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != "1.2.3.4" {
			t.Errorf("réponse %d incorrecte: %v", i+1, r.Answer)
		}
	}
}

func TestWithoutCacheEveryQueryGoesUpstream(t *testing.T) {
	upAddr, hits := startFakeUpstream(t)
	addr := startEfBlock(t, upAddr, filter.NewEngine(), nil)
	hits.Store(0)

	ask(t, addr, "example.com.", dns.TypeA)
	ask(t, addr, "example.com.", dns.TypeA)
	if n := hits.Load(); n != 2 {
		t.Errorf("l'upstream a reçu %d requêtes, attendu 2", n)
	}
}

func TestBlockedDomainNeverReachesUpstream(t *testing.T) {
	upAddr, hits := startFakeUpstream(t)
	eng := filter.NewEngine()
	eng.SetUserRules(nil, []string{"ads.example.com"})
	addr := startEfBlock(t, upAddr, eng, cache.New(100, 0))
	hits.Store(0)

	resp := ask(t, addr, "sub.ads.example.com.", dns.TypeA)
	if len(resp.Answer) != 1 || resp.Answer[0].(*dns.A).A.String() != "0.0.0.0" {
		t.Errorf("réponse attendue 0.0.0.0, obtenu %v", resp.Answer)
	}
	if hits.Load() != 0 {
		t.Error("un domaine bloqué ne doit jamais atteindre l'upstream")
	}
}

// Chaque requête doit apparaître dans le journal avec le bon statut.
func TestQueriesAreLogged(t *testing.T) {
	upAddr, _ := startFakeUpstream(t)
	eng := filter.NewEngine()
	eng.SetUserRules(nil, []string{"ads.example.com"})

	path := filepath.Join(t.TempDir(), "q.db")
	ql, err := querylog.Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	addr := startEfBlockLog(t, upAddr, eng, cache.New(100, 0), ql)

	ask(t, addr, "ads.example.com.", dns.TypeA) // bloquée
	ask(t, addr, "Example.COM.", dns.TypeA)     // transmise (nom en casse mixte)
	ask(t, addr, "example.com.", dns.TypeA)     // cache
	time.Sleep(50 * time.Millisecond)
	ql.Close() // écrit ce qui est en attente
	r, err := querylog.Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	got, _ := r.Recent(20)
	actions := map[string]string{} // domaine+statut
	for _, e := range got {
		if e.Name == "ready.test" {
			continue // requête de démarrage de startEfBlock
		}
		if e.Client != "127.0.0.1" {
			t.Errorf("client = %q, attendu 127.0.0.1", e.Client)
		}
		actions[e.Name+"/"+e.Action] = e.Rule
	}
	for _, want := range []string{"ads.example.com/blocked", "example.com/forwarded", "example.com/cached"} {
		if _, ok := actions[want]; !ok {
			t.Errorf("entrée manquante dans le journal: %s (obtenu %v)", want, actions)
		}
	}
	if actions["ads.example.com/blocked"] != "ads.example.com" {
		t.Errorf("la règle qui a bloqué doit être enregistrée, obtenu %q", actions["ads.example.com/blocked"])
	}
}
