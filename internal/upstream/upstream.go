// Package upstream envoie les requêtes DNS à un résolveur amont (1.1.1.1, 9.9.9.9...).
//
// Trois protocoles sont gérés, choisis par la forme de l'adresse dans la config :
//
//	"1.1.1.1" ou "1.1.1.1:53"             DNS classique (UDP, puis TCP si tronqué)
//	"tls://1.1.1.1"                       DoT : DNS dans une connexion TLS (port 853)
//	"https://cloudflare-dns.com/dns-query" DoH : DNS dans une requête HTTPS (port 443)
package upstream

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

// Resolver sait résoudre une requête DNS auprès d'un serveur amont.
type Resolver interface {
	Resolve(req *dns.Msg) (*dns.Msg, error)
	Name() string // pour les logs
}

// Forwarder essaie les résolveurs amont dans l'ordre jusqu'à obtenir une réponse.
//
// Il retient aussi lesquels sont en panne. Sans cette mémoire, un upstream qui
// ne répond plus (pare-feu qui jette les paquets, par exemple) ferait attendre
// le délai complet (3 s) CHAQUE requête avant de passer au suivant. Après un
// échec, on saute donc cet upstream pendant un moment, de plus en plus long si
// les échecs continuent (5 s, 10 s, 20 s... jusqu'à 1 minute).
type Forwarder struct {
	members []*member
	now     func() time.Time // horloge, remplaçable dans les tests
}

// member : un upstream et son état de santé.
type member struct {
	r         Resolver
	fails     atomic.Int32 // échecs consécutifs
	downUntil atomic.Int64 // jusqu'à quand on le saute (nanosecondes Unix)
}

const (
	backoffBase = 5 * time.Second
	backoffMax  = time.Minute
)

func (m *member) down(now time.Time) bool { return now.UnixNano() < m.downUntil.Load() }

func (m *member) markOK() {
	m.fails.Store(0)
	m.downUntil.Store(0)
}

func (m *member) markFailed(now time.Time) {
	n := m.fails.Add(1)
	d := backoffBase << min(n-1, 10) // 5 s, 10 s, 20 s...
	if d > backoffMax {
		d = backoffMax
	}
	m.downUntil.Store(now.Add(d).UnixNano())
}

// Status décrit l'état d'un upstream (affiché dans l'interface).
type Status struct {
	Name      string
	Healthy   bool
	Failures  int
	DownUntil time.Time // zéro si en bon état
}

// Status retourne l'état de chaque upstream.
func (f *Forwarder) Status() []Status {
	now := f.now()
	out := make([]Status, 0, len(f.members))
	for _, m := range f.members {
		st := Status{Name: m.r.Name(), Healthy: !m.down(now), Failures: int(m.fails.Load())}
		if !st.Healthy {
			st.DownUntil = time.Unix(0, m.downUntil.Load())
		}
		out = append(out, st)
	}
	return out
}

// New construit un Forwarder à partir des adresses de la config.
//
// bootstrap : serveurs DNS classiques ("ip:port") utilisés UNIQUEMENT pour
// trouver l'IP d'un upstream donné par son nom (ex: cloudflare-dns.com).
// Voir bootstrapDialer pour le pourquoi.
func New(specs []string, timeout time.Duration, bootstrap []string) (*Forwarder, error) {
	dialer := bootstrapDialer(bootstrap, timeout)
	f := &Forwarder{now: time.Now}
	for _, spec := range specs {
		r, err := newResolver(spec, timeout, dialer)
		if err != nil {
			return nil, fmt.Errorf("upstream %q: %w", spec, err)
		}
		f.members = append(f.members, &member{r: r})
	}
	if len(f.members) == 0 {
		return nil, errors.New("aucun upstream")
	}
	return f, nil
}

// Resolve envoie req aux upstreams en bon état, dans l'ordre, jusqu'au premier
// succès. Si aucun n'a répondu, il retente en dernier recours ceux qu'il avait
// sautés : mieux vaut un upstream peut-être revenu que de renoncer.
func (f *Forwarder) Resolve(req *dns.Msg) (*dns.Msg, error) {
	var errs []error
	var skipped []*member

	try := func(m *member) (*dns.Msg, bool) {
		resp, err := m.r.Resolve(req)
		if err != nil {
			m.markFailed(f.now())
			errs = append(errs, fmt.Errorf("%s: %w", m.r.Name(), err))
			return nil, false
		}
		m.markOK()
		return resp, true
	}

	now := f.now()
	for _, m := range f.members {
		if m.down(now) {
			skipped = append(skipped, m)
			continue
		}
		if resp, ok := try(m); ok {
			return resp, nil
		}
	}
	for _, m := range skipped {
		if resp, ok := try(m); ok {
			return resp, nil
		}
	}
	return nil, errors.Join(errs...)
}

// newResolver choisit l'implémentation selon le préfixe de l'adresse.
func newResolver(spec string, timeout time.Duration, dialer *net.Dialer) (Resolver, error) {
	switch {
	case strings.HasPrefix(spec, "tls://"):
		addr := withDefaultPort(strings.TrimPrefix(spec, "tls://"), "853")
		return newDoT(addr, nil, dialer, timeout), nil

	case strings.HasPrefix(spec, "https://"):
		u, err := url.Parse(spec)
		if err != nil || u.Host == "" {
			return nil, errors.New("URL DoH invalide")
		}
		return newDoH(spec, nil, dialer, timeout), nil

	case strings.Contains(spec, "://"):
		return nil, errors.New("protocole non géré (utilise tls://, https:// ou ip:port)")

	default:
		return newPlain(withDefaultPort(spec, "53"), timeout), nil
	}
}

// withDefaultPort ajoute le port par défaut si l'adresse n'en a pas.
func withDefaultPort(addr, port string) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(strings.Trim(addr, "[]"), port)
}

// TestResult : résultat du test d'un upstream.
type TestResult struct {
	Name      string
	OK        bool
	LatencyMs int64
	Error     string
}

// Test interroge chaque upstream pour de vrai ("example.com") et mesure le temps de
// réponse. Il sert à vérifier une configuration AVANT de l'enregistrer : un port
// bloqué par le réseau (853 pour DoT, par exemple) se voit tout de suite.
func Test(specs []string, timeout time.Duration, bootstrap []string) []TestResult {
	dialer := bootstrapDialer(bootstrap, timeout)
	out := make([]TestResult, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i].Name = spec
			r, err := newResolver(spec, timeout, dialer)
			if err != nil {
				out[i].Error = err.Error()
				return
			}
			out[i].Name = r.Name()
			start := time.Now()
			resp, err := r.Resolve(new(dns.Msg).SetQuestion("example.com.", dns.TypeA))
			out[i].LatencyMs = time.Since(start).Milliseconds()
			switch {
			case err != nil:
				out[i].Error = err.Error()
			case resp.Rcode != dns.RcodeSuccess:
				out[i].Error = "réponse " + dns.RcodeToString[resp.Rcode]
			default:
				out[i].OK = true
			}
		}()
	}
	wg.Wait()
	return out
}
