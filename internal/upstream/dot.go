package upstream

import (
	"crypto/tls"
	"net"
	"time"

	"github.com/miekg/dns"
)

// dot : DNS-over-TLS (RFC 7858). Les messages DNS sont les mêmes qu'en TCP,
// mais dans une connexion chiffrée sur le port 853. Quelqu'un qui observe le
// réseau (l'école, un FAI) voit qu'on parle à 1.1.1.1, mais plus QUELS
// domaines on demande, et ne peut plus modifier les réponses.
//
// Se connecter en TLS coûte un échange de poignées de main (~2 allers-retours).
// On garde donc quelques connexions ouvertes pour les réutiliser.
type dot struct {
	addr    string
	client  *dns.Client
	timeout time.Duration
	idle    chan idleConn
}

type idleConn struct {
	conn *dns.Conn
	at   time.Time
}

// maxIdle : un serveur coupe les connexions inactives au bout de quelques
// secondes. Au-delà de cette durée, on ne tente même pas de réutiliser.
const maxIdle = 10 * time.Second

func newDoT(addr string, tlsCfg *tls.Config, dialer *net.Dialer, timeout time.Duration) *dot {
	if tlsCfg == nil {
		// Le certificat du serveur est vérifié : c'est ce qui garantit qu'on
		// parle au vrai 1.1.1.1 et pas à un intrus. Le nom attendu est déduit de l'adresse.
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return &dot{
		addr:    addr,
		timeout: timeout,
		client:  &dns.Client{Net: "tcp-tls", Timeout: timeout, Dialer: dialer, TLSConfig: tlsCfg},
		idle:    make(chan idleConn, 4),
	}
}

func (d *dot) Name() string { return "tls://" + d.addr }

func (d *dot) Resolve(req *dns.Msg) (*dns.Msg, error) {
	// 1. On tente d'abord une connexion déjà ouverte.
	select {
	case ic := <-d.idle:
		if time.Since(ic.at) < maxIdle {
			if resp, err := d.exchange(ic.conn, req); err == nil {
				return resp, nil
			}
		}
		ic.conn.Close() // périmée ou cassée : on passe à une neuve
	default:
	}

	// 2. Sinon, nouvelle connexion TLS.
	conn, err := d.client.Dial(d.addr)
	if err != nil {
		return nil, err
	}
	return d.exchange(conn, req)
}

// exchange envoie req sur conn. En cas de succès, la connexion est remise
// dans le "pool" pour la requête suivante ; sinon elle est fermée.
func (d *dot) exchange(conn *dns.Conn, req *dns.Msg) (*dns.Msg, error) {
	resp, _, err := d.client.ExchangeWithConn(req, conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	select {
	case d.idle <- idleConn{conn: conn, at: time.Now()}:
	default:
		conn.Close() // pool plein
	}
	return resp, nil
}
