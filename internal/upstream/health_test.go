package upstream

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// blackhole reçoit les paquets mais ne répond jamais : un pare-feu qui jette le trafic.
func blackhole(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := pc.ReadFrom(buf); err != nil {
				return
			}
		}
	}()
	return pc.LocalAddr().String()
}

func goodServer(t *testing.T) string {
	t.Helper()
	addr, err := newUDP()
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	srv := &dns.Server{Addr: addr, Net: "udp", NotifyStartedFunc: func() { close(started) },
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) { _ = w.WriteMsg(answer(r)) })}
	go srv.ListenAndServe()
	<-started
	t.Cleanup(func() { srv.Shutdown() })
	return addr
}

func newUDP() (string, error) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer pc.Close()
	return pc.LocalAddr().String(), nil
}

// fakeClock : horloge manuelle, pour tester les délais d'attente sans attendre.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func timed(f *Forwarder) (time.Duration, error) {
	start := time.Now()
	_, err := f.Resolve(new(dns.Msg).SetQuestion("example.com.", dns.TypeA))
	return time.Since(start), err
}

func TestDeadUpstreamIsSkippedAfterFirstFailure(t *testing.T) {
	const timeout = 300 * time.Millisecond
	f, err := New([]string{blackhole(t), goodServer(t)}, timeout, nil)
	if err != nil {
		t.Fatal(err)
	}
	clk := &fakeClock{t: time.Unix(1_000_000, 0)}
	f.now = clk.now

	// 1re requête : le premier upstream est essayé et fait perdre le délai complet.
	d, err := timed(f)
	if err != nil || d < timeout-50*time.Millisecond {
		t.Fatalf("1re requête: %v, err=%v (attendu ~%v puis succès via le 2e upstream)", d, err, timeout)
	}
	if st := f.Status(); st[0].Healthy || !st[1].Healthy || st[0].Failures != 1 {
		t.Errorf("le 1er upstream devrait être marqué en panne: %+v", st)
	}

	// Requêtes suivantes : l'upstream en panne est sauté, donc c'est rapide.
	for i := 0; i < 3; i++ {
		if d, err := timed(f); err != nil || d > timeout/2 {
			t.Errorf("requête %d: %v, err=%v, attendu quasi instantané", i+2, d, err)
		}
	}

	// Après le délai (5 s), on le retente : il échoue encore, la pénalité double (10 s).
	clk.advance(6 * time.Second)
	if d, _ := timed(f); d < timeout-50*time.Millisecond {
		t.Errorf("après 6 s il doit être retenté (et faire attendre): %v", d)
	}
	clk.advance(6 * time.Second) // 6 s < 10 s : encore sauté
	if d, _ := timed(f); d > timeout/2 {
		t.Errorf("la pénalité doublée (10 s) n'est pas respectée: %v", d)
	}
	if st := f.Status(); st[0].Failures != 2 {
		t.Errorf("échecs consécutifs = %d, attendu 2", st[0].Failures)
	}
}

// Si tous les upstreams sont marqués en panne, on retente quand même : renoncer serait pire.
func TestAllDownStillRetries(t *testing.T) {
	const timeout = 150 * time.Millisecond
	f, _ := New([]string{blackhole(t), blackhole(t)}, timeout, nil)

	if d, err := timed(f); err == nil || d < timeout {
		t.Fatalf("1re requête: %v err=%v, attendu un échec après les délais", d, err)
	}
	// Les deux sont maintenant marqués en panne ; la requête suivante doit tout de même les essayer.
	if d, err := timed(f); err == nil || d < timeout {
		t.Errorf("2e requête: %v err=%v, attendu une nouvelle tentative réelle (dernier recours)", d, err)
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	m := &member{}
	now := time.Unix(1_000_000, 0)
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, time.Minute, time.Minute}
	for i, w := range want {
		m.markFailed(now)
		if got := time.Duration(m.downUntil.Load() - now.UnixNano()); got != w {
			t.Errorf("échec %d: pénalité %v, attendu %v", i+1, got, w)
		}
	}
	m.markOK()
	if m.down(now) || m.fails.Load() != 0 {
		t.Error("un succès doit tout remettre à zéro")
	}
}
