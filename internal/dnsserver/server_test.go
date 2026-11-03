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
	"github.com/x3Lo/efBlock/internal/localdns"
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

	got, _ := r.Recent(20, "")
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

// Un client hors des réseaux autorisés reçoit REFUSED et n'atteint jamais l'upstream.
func TestClientAccessControl(t *testing.T) {
	upAddr, hits := startFakeUpstream(t)

	run := func(nets []string) *dns.Msg {
		fwd, _ := upstream.New([]string{upAddr}, 2*time.Second, nil)
		var parsed []*net.IPNet
		for _, c := range nets {
			_, n, _ := net.ParseCIDR(c)
			parsed = append(parsed, n)
		}
		addr := freeAddr(t)
		srv := New(Options{Addr: addr, Upstream: fwd, Filter: filter.NewEngine(), AllowedClients: parsed})
		go srv.Start()
		t.Cleanup(srv.Shutdown)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			m := new(dns.Msg).SetQuestion("example.com.", dns.TypeA)
			if r, err := dns.Exchange(m, addr); err == nil {
				return r
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("le serveur n'a pas répondu")
		return nil
	}

	// Les requêtes de test viennent de 127.0.0.1 : autorisé par 127.0.0.0/8, refusé par 192.168.0.0/16.
	hits.Store(0)
	if r := run([]string{"192.168.0.0/16"}); r.Rcode != dns.RcodeRefused || len(r.Answer) != 0 {
		t.Errorf("client hors réseau: rcode=%s, attendu REFUSED", dns.RcodeToString[r.Rcode])
	}
	if hits.Load() != 0 {
		t.Error("un client refusé ne doit jamais déclencher de requête vers l'upstream")
	}
	if r := run([]string{"192.168.0.0/16", "127.0.0.0/8"}); r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 {
		t.Errorf("client autorisé: rcode=%s, attendu NOERROR avec une réponse", dns.RcodeToString[r.Rcode])
	}
}

// Les réglages changent à chaud, sans redémarrer le serveur.
func TestHotReconfiguration(t *testing.T) {
	upA, hitsA := startFakeUpstream(t)
	upB, hitsB := startFakeUpstream(t)

	fwdA, _ := upstream.New([]string{upA}, time.Second, nil)
	fwdB, _ := upstream.New([]string{upB}, time.Second, nil)
	addr := freeAddr(t)
	srv := New(Options{Addr: addr, Upstream: fwdA, Filter: filter.NewEngine()})
	go srv.Start()
	t.Cleanup(srv.Shutdown)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := dns.Exchange(new(dns.Msg).SetQuestion("ready.test.", dns.TypeA), addr); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	hitsA.Store(0)

	ask(t, addr, "un.example.com.", dns.TypeA)
	if hitsA.Load() != 1 || hitsB.Load() != 0 {
		t.Fatalf("avant: A=%d B=%d", hitsA.Load(), hitsB.Load())
	}

	// 1. Changement d'upstream.
	srv.SetUpstream(fwdB)
	ask(t, addr, "deux.example.com.", dns.TypeA)
	if hitsB.Load() != 1 {
		t.Errorf("après SetUpstream, l'upstream B aurait dû recevoir la requête (B=%d)", hitsB.Load())
	}

	// 2. Activation du cache : la 2e requête identique ne sort plus.
	srv.SetCache(cache.New(100, 0))
	ask(t, addr, "trois.example.com.", dns.TypeA)
	ask(t, addr, "trois.example.com.", dns.TypeA)
	if hitsB.Load() != 2 {
		t.Errorf("avec le cache, 2 requêtes identiques ne doivent faire qu'un aller-retour (B=%d, attendu 2 au total)", hitsB.Load())
	}
	srv.SetCache(nil)
	ask(t, addr, "trois.example.com.", dns.TypeA)
	if hitsB.Load() != 3 {
		t.Errorf("cache retiré : la requête doit repartir (B=%d)", hitsB.Load())
	}

	// 3. Noms locaux : répondus sans toucher l'upstream.
	z, err := localdns.New("", nil, []string{"192.168.1.20 nas.maison"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetLocal(z)
	before := hitsB.Load()
	r := ask(t, addr, "nas.maison.", dns.TypeA)
	if len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != "192.168.1.20" || hitsB.Load() != before {
		t.Errorf("nom local: %v (B passé de %d à %d)", r.Answer, before, hitsB.Load())
	}
	// Le cas observé en vrai : la recherche inverse d'une IP privée ne doit pas sortir.
	r = ask(t, addr, "10.50.168.192.in-addr.arpa.", dns.TypePTR)
	if r.Rcode != dns.RcodeNameError || hitsB.Load() != before {
		t.Errorf("PTR privé : rcode=%s, requêtes envoyées à l'upstream: %d (attendu NXDOMAIN, 0)", dns.RcodeToString[r.Rcode], hitsB.Load()-before)
	}

	// 4. Mode de blocage.
	srv.opts.Filter.SetUserRules(nil, []string{"pub.example.com"})
	if r := ask(t, addr, "pub.example.com.", dns.TypeA); len(r.Answer) != 1 || r.Rcode != dns.RcodeSuccess {
		t.Errorf("mode 0.0.0.0: %+v", r)
	}
	srv.SetNXDomain(true)
	if r := ask(t, addr, "pub.example.com.", dns.TypeA); r.Rcode != dns.RcodeNameError {
		t.Errorf("mode NXDOMAIN: rcode=%s", dns.RcodeToString[r.Rcode])
	}
}
