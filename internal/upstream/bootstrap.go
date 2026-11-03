package upstream

import (
	"context"
	"net"
	"time"
)

// bootstrapDialer retourne un net.Dialer qui résout les noms de domaine avec
// les serveurs "bootstrap" plutôt qu'avec le DNS du système.
//
// Concept : le problème de l'œuf et de la poule. Pour parler à
// "cloudflare-dns.com" en DoH, il faut d'abord connaître son IP... donc faire
// une requête DNS. Mais efBlock EST le serveur DNS du réseau : il ne peut pas
// se demander à lui-même. On utilise donc quelques serveurs DNS classiques, en
// clair, uniquement pour cette première résolution. Si tes upstreams sont
// donnés par IP ("tls://1.1.1.1"), cette étape n'est jamais utilisée.
func bootstrapDialer(servers []string, timeout time.Duration) *net.Dialer {
	d := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	if len(servers) == 0 {
		return d // DNS du système
	}
	d.Resolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var last error
			for _, s := range servers {
				c, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, network, s)
				if err == nil {
					return c, nil
				}
				last = err
			}
			return nil, last
		},
	}
	return d
}
