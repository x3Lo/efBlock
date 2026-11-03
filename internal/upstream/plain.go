package upstream

import (
	"time"

	"github.com/miekg/dns"
)

// plain : DNS classique, en clair, sur le port 53.
type plain struct {
	addr    string
	timeout time.Duration
}

func newPlain(addr string, timeout time.Duration) *plain {
	return &plain{addr: addr, timeout: timeout}
}

func (p *plain) Name() string { return p.addr }

// Resolve fait un aller-retour avec le serveur.
//
// Concept DNS : en UDP, une réponse est limitée en taille (512 octets sans
// EDNS0). Si elle ne tient pas, le serveur met le drapeau TC ("truncated")
// et on doit refaire la même question en TCP pour obtenir la réponse complète.
func (p *plain) Resolve(req *dns.Msg) (*dns.Msg, error) {
	udp := &dns.Client{Net: "udp", Timeout: p.timeout}
	resp, _, err := udp.Exchange(req, p.addr)
	if err != nil {
		return nil, err
	}
	if resp.Truncated {
		tcp := &dns.Client{Net: "tcp", Timeout: p.timeout}
		resp, _, err = tcp.Exchange(req, p.addr)
		if err != nil {
			return nil, err
		}
	}
	return resp, nil
}
