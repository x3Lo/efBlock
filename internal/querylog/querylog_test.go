package querylog

import (
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T, retention time.Duration) *Logger {
	t.Helper()
	l, err := Open(filepath.Join(t.TempDir(), "q.db"), retention)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func entry(client, name, action string, age time.Duration) Entry {
	return Entry{Time: time.Now().Add(-age), Client: client, Name: name, QType: "A", Action: action,
		Rcode: "NOERROR", Duration: 2 * time.Millisecond}
}

// flushed : Close() vide la file d'attente ; on rouvre ensuite pour lire.
func reopen(t *testing.T, l *Logger, path string) *Logger {
	t.Helper()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l2, err := Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l2.Close() })
	return l2
}

func TestStatsAndTopLists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.db")
	l, err := Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		l.Log(entry("192.168.50.20", "ads.example.com", ActionBlocked, 0))
	}
	l.Log(entry("192.168.50.20", "tracker.example.net", ActionBlocked, 0))
	l.Log(entry("192.168.50.21", "example.org", ActionForwarded, 0))
	l.Log(entry("192.168.50.21", "example.org", ActionCached, 0))
	l.Log(entry("192.168.50.21", "example.org", ActionCached, 0))
	l.Log(entry("192.168.50.20", "broken.example", ActionError, 0))

	r := reopen(t, l, path) // la fermeture écrit tout ce qui était en attente

	s, err := r.Summary(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := Summary{Total: 8, Blocked: 4, Cached: 2, Forwarded: 1, Errors: 1}
	if s != want {
		t.Errorf("Summary = %+v, attendu %+v", s, want)
	}
	if p := s.BlockedPercent(); p != 50 {
		t.Errorf("BlockedPercent = %v, attendu 50", p)
	}

	blocked, _ := r.TopBlocked(time.Now().Add(-time.Hour), 10)
	if len(blocked) != 2 || blocked[0] != (Count{"ads.example.com", 3}) || blocked[1] != (Count{"tracker.example.net", 1}) {
		t.Errorf("TopBlocked = %+v", blocked)
	}
	domains, _ := r.TopDomains(time.Now().Add(-time.Hour), 10)
	if len(domains) != 2 || domains[0] != (Count{"example.org", 3}) { // les bloqués n'y sont pas
		t.Errorf("TopDomains = %+v", domains)
	}
	clients, _ := r.TopClients(time.Now().Add(-time.Hour), 1)
	if len(clients) != 1 || clients[0] != (Count{"192.168.50.20", 5}) { // limit=1
		t.Errorf("TopClients = %+v", clients)
	}

	// Une période qui commence dans le futur ne contient rien.
	if s, _ := r.Summary(time.Now().Add(time.Hour)); s.Total != 0 {
		t.Errorf("Summary dans le futur = %+v", s)
	}
}

func TestRecentOrderAndFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.db")
	l, _ := Open(path, 0)
	l.Log(entry("c1", "old.example.com", ActionForwarded, 10*time.Second))
	e := entry("c2", "new.example.com", ActionBlocked, 0)
	e.Rule, e.QType = "example.com", "AAAA"
	l.Log(e)
	r := reopen(t, l, path)

	got, err := r.Recent(10, "")
	if err != nil || len(got) != 2 {
		t.Fatalf("Recent: %v, %d lignes", err, len(got))
	}
	if got[0].Name != "new.example.com" || got[1].Name != "old.example.com" {
		t.Errorf("ordre incorrect: %s puis %s", got[0].Name, got[1].Name)
	}
	if got[0].Rule != "example.com" || got[0].QType != "AAAA" || got[0].Client != "c2" || got[0].Duration != 2*time.Millisecond {
		t.Errorf("champs incorrects: %+v", got[0])
	}
	if one, _ := r.Recent(1, ""); len(one) != 1 {
		t.Error("la limite n'est pas respectée")
	}
}

func TestRetentionPurge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.db")
	l, _ := Open(path, 0)
	l.Log(entry("c", "vieux.example.com", ActionForwarded, 48*time.Hour))
	l.Log(entry("c", "recent.example.com", ActionForwarded, time.Minute))
	l.Close()

	// Rouvrir avec 24 h de rétention purge immédiatement les lignes plus vieilles.
	r, err := Open(path, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, _ := r.Recent(10, "")
	if len(got) != 1 || got[0].Name != "recent.example.com" {
		t.Errorf("après purge: %+v", got)
	}
}

func TestNilLoggerAndDoubleClose(t *testing.T) {
	var nilLogger *Logger
	nilLogger.Log(Entry{}) // ne doit pas paniquer : logging désactivé
	if err := nilLogger.Close(); err != nil {
		t.Error(err)
	}

	l := openTest(t, 0)
	l.Close()
	l.Log(entry("c", "apres-fermeture.example.com", ActionForwarded, 0)) // ne doit pas paniquer
	if err := l.Close(); err != nil {
		t.Errorf("double Close: %v", err)
	}
}

// Log() ne doit jamais bloquer, même si l'écrivain est à l'arrêt ou trop lent.
func TestLogNeverBlocks(t *testing.T) {
	l := &Logger{ch: make(chan Entry, 2)} // file minuscule, personne ne la vide
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			l.Log(entry("c", "x.example.com", ActionForwarded, 0))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Log() a bloqué")
	}
	if l.dropped.Load() != 98 {
		t.Errorf("abandonnées = %d, attendu 98", l.dropped.Load())
	}
}

func TestRecentSearchAndTimeline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.db")
	l, _ := Open(path, 0)
	l.Log(entry("192.168.50.20", "ads_tracker.example.com", ActionBlocked, 0))
	l.Log(entry("192.168.50.21", "adsXtracker.example.com", ActionForwarded, 0)) // "_" ne doit pas servir de joker
	l.Log(entry("192.168.50.21", "100%.example.com", ActionForwarded, 0))
	l.Log(entry("192.168.50.20", "ancien.example.com", ActionForwarded, 3*time.Hour))
	r := reopen(t, l, path)

	got, _ := r.Recent(10, "ads_tracker")
	if len(got) != 1 || got[0].Name != "ads_tracker.example.com" {
		t.Errorf("recherche avec _ : %+v", got)
	}
	if got, _ := r.Recent(10, "100%"); len(got) != 1 {
		t.Errorf("recherche avec %% : %+v", got)
	}
	if got, _ := r.Recent(10, "192.168.50.21"); len(got) != 2 {
		t.Errorf("recherche par client : %d résultats", len(got))
	}
	if got, _ := r.Recent(10, "ADS_TRACKER"); len(got) != 1 {
		t.Errorf("la recherche doit ignorer la casse")
	}

	buckets, err := r.Timeline(time.Now().Add(-4*time.Hour), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) < 4 || len(buckets) > 6 {
		t.Fatalf("%d tranches, attendu ~5", len(buckets))
	}
	var total, blocked int64
	for _, b := range buckets {
		total += b.Total
		blocked += b.Blocked
	}
	if total != 4 || blocked != 1 {
		t.Errorf("total=%d blocked=%d, attendu 4 et 1", total, blocked)
	}
}

func TestSubscribeReceivesLiveEntries(t *testing.T) {
	l := openTest(t, 0)
	ch, cancel := l.Subscribe()
	defer cancel()

	l.Log(entry("c1", "live.example.com", ActionBlocked, 0))
	select {
	case e := <-ch:
		if e.Name != "live.example.com" || e.Action != ActionBlocked {
			t.Errorf("entrée reçue: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("aucune entrée reçue en direct")
	}

	cancel()
	cancel() // double annulation sans effet
	if _, ok := <-ch; ok {
		t.Error("le canal doit être fermé après désabonnement")
	}
	l.Log(entry("c1", "apres.example.com", ActionForwarded, 0)) // ne doit pas paniquer
}

// Un abonné qui ne lit jamais ne doit pas ralentir Log().
func TestSlowSubscriberNeverBlocksLog(t *testing.T) {
	l := openTest(t, 0)
	_, cancel := l.Subscribe() // personne ne lit ce canal
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < subBuffer*3; i++ {
			l.Log(entry("c", "x.example.com", ActionForwarded, 0))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("un abonné lent a bloqué Log()")
	}
}

func TestCloseEndsSubscriptions(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "q.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := l.Subscribe()
	l.Close()
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("canal inattendu: une entrée au lieu d'une fermeture")
		}
	case <-time.After(time.Second):
		t.Fatal("Close() doit fermer les flux en direct")
	}
}
