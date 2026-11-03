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
	"time"

	"github.com/miekg/dns"
)

// Resolver sait résoudre une requête DNS auprès d'un serveur amont.
type Resolver interface {
	Resolve(req *dns.Msg) (*dns.Msg, error)
	Name() string // pour les logs
}

// Forwarder essaie les résolveurs amont dans l'ordre jusqu'à obtenir une
// réponse (basculement simple en cas de panne).
type Forwarder struct {
	resolvers []Resolver
}

// New construit un Forwarder à partir des adresses de la config.
//
// bootstrap : serveurs DNS classiques ("ip:port") utilisés UNIQUEMENT pour
// trouver l'IP d'un upstream donné par son nom (ex: cloudflare-dns.com).
// Voir bootstrapDialer pour le pourquoi.
func New(specs []string, timeout time.Duration, bootstrap []string) (*Forwarder, error) {
	dialer := bootstrapDialer(bootstrap, timeout)
	f := &Forwarder{}
	for _, spec := range specs {
		r, err := newResolver(spec, timeout, dialer)
		if err != nil {
			return nil, fmt.Errorf("upstream %q: %w", spec, err)
		}
		f.resolvers = append(f.resolvers, r)
	}
	if len(f.resolvers) == 0 {
		return nil, errors.New("aucun upstream")
	}
	return f, nil
}

// Resolve envoie req à chaque upstream jusqu'au premier succès.
func (f *Forwarder) Resolve(req *dns.Msg) (*dns.Msg, error) {
	var errs []error
	for _, r := range f.resolvers {
		resp, err := r.Resolve(req)
		if err == nil {
			return resp, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", r.Name(), err))
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
