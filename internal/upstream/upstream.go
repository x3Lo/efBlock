// Package upstream envoie les requêtes DNS à un résolveur amont (1.1.1.1, 9.9.9.9...).
package upstream

import (
	"errors"
	"fmt"
	"time"

	"github.com/miekg/dns"
)

// Forwarder transmet une requête aux résolveurs amont, dans l'ordre,
// jusqu'à obtenir une réponse (basculement simple en cas de panne).
type Forwarder struct {
	servers []string // "ip:port"
	timeout time.Duration
}

// New crée un Forwarder.
func New(servers []string, timeout time.Duration) *Forwarder {
	return &Forwarder{servers: servers, timeout: timeout}
}

// Resolve envoie req à chaque upstream jusqu'au premier succès.
func (f *Forwarder) Resolve(req *dns.Msg) (*dns.Msg, error) {
	var errs []error
	for _, server := range f.servers {
		resp, err := f.exchange(req, server)
		if err == nil {
			return resp, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", server, err))
	}
	return nil, errors.Join(errs...)
}

// exchange fait un aller-retour avec un upstream.
//
// Concept DNS : en UDP, une réponse est limitée en taille (512 octets sans
// EDNS0). Si elle ne tient pas, le serveur met le drapeau TC ("truncated")
// et on doit refaire la même question en TCP pour obtenir la réponse complète.
func (f *Forwarder) exchange(req *dns.Msg, server string) (*dns.Msg, error) {
	udp := &dns.Client{Net: "udp", Timeout: f.timeout}
	resp, _, err := udp.Exchange(req, server)
	if err != nil {
		return nil, err
	}
	if resp.Truncated {
		tcp := &dns.Client{Net: "tcp", Timeout: f.timeout}
		resp, _, err = tcp.Exchange(req, server)
		if err != nil {
			return nil, err
		}
	}
	return resp, nil
}
