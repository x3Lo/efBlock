package dnsserver

import (
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/x3Lo/efBlock/internal/cache"
	"github.com/x3Lo/efBlock/internal/filter"
	"github.com/x3Lo/efBlock/internal/querylog"
	"github.com/x3Lo/efBlock/internal/upstream"
)

// startUpstream lance un faux résolveur dont la réponse est fabriquée par answer.
// stop() l'arrête (pour simuler une panne).
func startUpstream(t *testing.T, answer func(req *dns.Msg) *dns.Msg) (addr string, hits *atomic.Int32, stop func()) {
	t.Helper()
	addr = freeAddr(t)
	hits = new(atomic.Int32)
	started := make(chan struct{})
	srv := &dns.Server{Addr: addr, Net: "udp", NotifyStartedFunc: func() { close(started) },
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			hits.Add(1)
			_ = w.WriteMsg(answer(req))
		})}
	go srv.ListenAndServe()
	<-started
	var once sync.Once
	stop = func() { once.Do(func() { srv.Shutdown() }) }
	t.Cleanup(stop)
	return addr, hits, stop
}

func aRecord(name string, ip string, ttl uint32) dns.RR {
	return &dns.A{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl}, A: net.ParseIP(ip)}
}

// cnameChain répond "metrics.monsite.example.org -> collecte.traqueur.example.net -> 5.6.7.8".
func cnameChain(ttl uint32) func(*dns.Msg) *dns.Msg {
	return func(req *dns.Msg) *dns.Msg {
		resp := new(dns.Msg)
		resp.SetReply(req)
		q := req.Question[0].Name
		resp.Answer = []dns.RR{
			&dns.CNAME{Hdr: dns.RR_Header{Name: q, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: ttl}, Target: "collecte.traqueur.example.net."},
			aRecord("collecte.traqueur.example.net.", "5.6.7.8", ttl),
		}
		return resp
	}
}

// start démarre efBlock avec un moteur et un cache donnés ; retourne son adresse et son serveur.
func start(t *testing.T, upAddr string, eng *filter.Engine, c *cache.Cache, l *querylog.Logger) (string, *Server) {
	t.Helper()
	fwd, err := upstream.New([]string{upAddr}, 500*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	addr := freeAddr(t)
	srv := New(Options{Addr: addr, Upstream: fwd, Filter: eng, Cache: c, Log: l})
	go srv.Start()
	t.Cleanup(srv.Shutdown)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := dns.Exchange(new(dns.Msg).SetQuestion("ready.test.", dns.TypeA), addr); err == nil {
			return addr, srv
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("le serveur n'a pas démarré")
	return "", nil
}

func firstA(r *dns.Msg) string {
	for _, rr := range r.Answer {
		if a, ok := rr.(*dns.A); ok {
			return a.A.String()
		}
	}
	return ""
}

// --- CNAME cloaking ---

func TestCNAMECloakingIsBlocked(t *testing.T) {
	up, _, _ := startUpstream(t, cnameChain(60))
	eng := filter.NewEngine()
	eng.SetUserRules(nil, []string{"traqueur.example.net"})
	path := filepath.Join(t.TempDir(), "q.db")
	ql, _ := querylog.Open(path, 0)
	addr, _ := start(t, up, eng, cache.New(100, 0), ql)

	// Le nom demandé est innocent, mais il pointe vers un traqueur : réponse bloquée.
	r := ask(t, addr, "metrics.monsite.example.org.", dns.TypeA)
	if firstA(r) != "0.0.0.0" {
		t.Fatalf("la réponse à un alias de traqueur doit être bloquée (0.0.0.0), obtenu %v", r.Answer)
	}
	// Idem pour une réponse déjà en cache.
	if r := ask(t, addr, "metrics.monsite.example.org.", dns.TypeA); firstA(r) != "0.0.0.0" {
		t.Errorf("depuis le cache aussi: %v", r.Answer)
	}

	// Le journal indique que c'est un alias qui a causé le blocage.
	time.Sleep(50 * time.Millisecond)
	ql.Close()
	reopened, _ := querylog.Open(path, 0)
	defer reopened.Close()
	rows, _ := reopened.Recent(10, "metrics")
	if len(rows) == 0 || rows[0].Action != querylog.ActionBlocked || !strings.HasPrefix(rows[0].Rule, "CNAME collecte.traqueur.example.net") {
		t.Errorf("journal: %+v", rows)
	}

	// La règle est retirée : la réponse en cache est de nouveau servie telle quelle, tout de suite.
	eng.SetUserRules(nil, nil)
	if r := ask(t, addr, "metrics.monsite.example.org.", dns.TypeA); firstA(r) != "5.6.7.8" {
		t.Errorf("sans règle, la vraie réponse doit repasser immédiatement: %v", r.Answer)
	}
}

func TestCNAMECheckHonoursAllowAndSwitch(t *testing.T) {
	up, _, _ := startUpstream(t, cnameChain(60))
	eng := filter.NewEngine()
	eng.SetUserRules([]string{"metrics.monsite.example.org"}, []string{"traqueur.example.net"})
	addr, _ := start(t, up, eng, nil, nil)

	if r := ask(t, addr, "metrics.monsite.example.org.", dns.TypeA); firstA(r) != "5.6.7.8" {
		t.Errorf("un nom que tu autorises explicitement n'est pas bloqué via ses alias: %v", r.Answer)
	}

	eng.SetUserRules(nil, []string{"traqueur.example.net"})
	eng.SetCNAMECheck(false)
	if r := ask(t, addr, "autre.monsite.example.org.", dns.TypeA); firstA(r) != "5.6.7.8" {
		t.Errorf("vérification des alias désactivée: la réponse passe: %v", r.Answer)
	}
	eng.SetCNAMECheck(true)
	if r := ask(t, addr, "autre.monsite.example.org.", dns.TypeA); firstA(r) != "0.0.0.0" {
		t.Errorf("vérification réactivée: %v", r.Answer)
	}
}

// --- Groupes de clients ---

func TestClientGroupsAtServerLevel(t *testing.T) {
	up, hits, _ := startUpstream(t, func(req *dns.Msg) *dns.Msg {
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Answer = []dns.RR{aRecord(req.Question[0].Name, "1.2.3.4", 60)}
		return resp
	})
	eng := filter.NewEngine()
	eng.SetUserRules(nil, []string{"pub.example.com"})
	addr, _ := start(t, up, eng, nil, nil)
	hits.Store(0)

	if r := ask(t, addr, "pub.example.com.", dns.TypeA); firstA(r) != "0.0.0.0" {
		t.Fatalf("sans groupe: bloqué, obtenu %v", r.Answer)
	}
	// Les requêtes de test viennent de 127.0.0.1 : ce groupe n'a aucun blocage.
	eng.SetGroups([]filter.GroupSpec{{Name: "Libre", Clients: []string{"127.0.0.1"}, Off: true}})
	if r := ask(t, addr, "pub.example.com.", dns.TypeA); firstA(r) != "1.2.3.4" {
		t.Errorf("client d'un groupe sans blocage: la vraie réponse est attendue, obtenu %v", r.Answer)
	}
	if hits.Load() != 1 {
		t.Errorf("l'upstream aurait dû être interrogé une fois (%d)", hits.Load())
	}
	// Un groupe qui ne contient pas ce client ne change rien.
	eng.SetGroups([]filter.GroupSpec{{Name: "Autres", Clients: []string{"192.168.77.0/24"}, Off: true}})
	if r := ask(t, addr, "pub.example.com.", dns.TypeA); firstA(r) != "0.0.0.0" {
		t.Errorf("client hors du groupe: bloqué, obtenu %v", r.Answer)
	}
}

// --- Réponse périmée ---

func TestStaleAnswerServedWhenUpstreamIsDown(t *testing.T) {
	up, _, stop := startUpstream(t, func(req *dns.Msg) *dns.Msg {
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Answer = []dns.RR{aRecord(req.Question[0].Name, "1.2.3.4", 1)} // TTL d'1 seconde
		return resp
	})
	c := cache.New(100, 0)
	c.SetStale(time.Hour)
	addr, _ := start(t, up, filter.NewEngine(), c, nil)

	if r := ask(t, addr, "site.example.com.", dns.TypeA); firstA(r) != "1.2.3.4" {
		t.Fatalf("1re réponse: %v", r.Answer)
	}
	stop()                              // tous les upstreams tombent
	time.Sleep(1300 * time.Millisecond) // la réponse du cache est maintenant périmée

	r := ask(t, addr, "site.example.com.", dns.TypeA)
	if r.Rcode != dns.RcodeSuccess || firstA(r) != "1.2.3.4" {
		t.Fatalf("avec les upstreams en panne, la réponse périmée doit être servie: rcode=%s %v", dns.RcodeToString[r.Rcode], r.Answer)
	}
	if ttl := r.Answer[0].Header().Ttl; ttl > 30 {
		t.Errorf("TTL = %d, attendu 30 au plus", ttl)
	}
	// Un domaine jamais vu, lui, échoue normalement.
	if r := ask(t, addr, "jamais-vu.example.com.", dns.TypeA); r.Rcode != dns.RcodeServerFailure {
		t.Errorf("domaine inconnu: rcode=%s, attendu SERVFAIL", dns.RcodeToString[r.Rcode])
	}
}

func TestNoStaleWithoutOptIn(t *testing.T) {
	up, _, stop := startUpstream(t, func(req *dns.Msg) *dns.Msg {
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Answer = []dns.RR{aRecord(req.Question[0].Name, "1.2.3.4", 1)}
		return resp
	})
	addr, _ := start(t, up, filter.NewEngine(), cache.New(100, 0), nil) // secours non activé
	ask(t, addr, "site.example.com.", dns.TypeA)
	stop()
	time.Sleep(1300 * time.Millisecond)
	if r := ask(t, addr, "site.example.com.", dns.TypeA); r.Rcode != dns.RcodeServerFailure {
		t.Errorf("sans réponse périmée activée, SERVFAIL attendu, obtenu %s", dns.RcodeToString[r.Rcode])
	}
}

// --- Fusion des requêtes identiques ---

func TestIdenticalConcurrentQueriesAreMerged(t *testing.T) {
	up, hits, _ := startUpstream(t, func(req *dns.Msg) *dns.Msg {
		time.Sleep(300 * time.Millisecond) // le temps que les autres demandes arrivent
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Answer = []dns.RR{aRecord(req.Question[0].Name, "1.2.3.4", 60)}
		return resp
	})
	addr, _ := start(t, up, filter.NewEngine(), nil, nil) // pas de cache : seule la fusion agit
	hits.Store(0)

	const n = 10
	var wg sync.WaitGroup
	ids := make([]uint16, n)
	got := make([]uint16, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := new(dns.Msg).SetQuestion("populaire.example.com.", dns.TypeA)
			ids[i] = m.Id
			r, err := dns.Exchange(m, addr)
			if err != nil || firstA(r) != "1.2.3.4" {
				t.Errorf("demande %d: %v %v", i, err, r)
				return
			}
			got[i] = r.Id
		}()
	}
	wg.Wait()

	if h := hits.Load(); h > 2 {
		t.Errorf("%d requêtes identiques simultanées ont produit %d requêtes vers l'upstream (attendu 1, au plus 2)", n, h)
	}
	for i := range ids {
		if ids[i] != got[i] {
			t.Errorf("demande %d : chaque client doit recevoir SON identifiant (envoyé %d, reçu %d)", i, ids[i], got[i])
		}
	}
}

// --- Préchargement ---

func TestPrefetchRefreshesTheCache(t *testing.T) {
	up, hits, _ := startUpstream(t, func(req *dns.Msg) *dns.Msg {
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Answer = []dns.RR{aRecord(req.Question[0].Name, "9.9.9.9", 60)}
		return resp
	})
	c := cache.New(100, 0)
	_, srv := start(t, up, filter.NewEngine(), c, nil)
	hits.Store(0)

	req := new(dns.Msg).SetQuestion("chaud.example.com.", dns.TypeA)
	if _, ok := c.Get(req); ok {
		t.Fatal("le cache doit être vide au départ")
	}
	srv.prefetch(req, c)
	if hits.Load() != 1 {
		t.Errorf("le préchargement doit interroger l'upstream une fois (%d)", hits.Load())
	}
	if r, ok := c.Get(req); !ok || firstA(r) != "9.9.9.9" {
		t.Errorf("la réponse préchargée doit être en cache: %v %v", r, ok)
	}
}
