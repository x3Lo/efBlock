package upstream

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/miekg/dns"
)

// doh : DNS-over-HTTPS (RFC 8484). Le message DNS est envoyé tel quel (format
// binaire) dans le corps d'une requête HTTPS POST. Sur le réseau, ça ressemble
// à du trafic web ordinaire sur le port 443 : c'est quasiment impossible à
// bloquer sans couper le web, d'où son intérêt sur un réseau filtrant.
type doh struct {
	url    string
	client *http.Client
}

const dohContentType = "application/dns-message"

func newDoH(url string, tlsCfg *tls.Config, dialer *net.Dialer, timeout time.Duration) *doh {
	tr := &http.Transport{
		DialContext:         dialer.DialContext, // résolution via le bootstrap
		TLSClientConfig:     tlsCfg,
		ForceAttemptHTTP2:   true, // une seule connexion, plusieurs requêtes en parallèle
		TLSHandshakeTimeout: timeout,
		IdleConnTimeout:     60 * time.Second,
		MaxIdleConnsPerHost: 4,
		// Pas de Proxy : le DNS ne doit pas dépendre des variables HTTP_PROXY.
	}
	return &doh{url: url, client: &http.Client{Transport: tr, Timeout: timeout}}
}

func (d *doh) Name() string { return d.url }

func (d *doh) Resolve(req *dns.Msg) (*dns.Msg, error) {
	// RFC 8484 recommande un ID à 0 : deux requêtes identiques donnent alors
	// la même requête HTTP, ce qui aide les caches. On rétablit l'ID après.
	origID := req.Id
	req.Id = 0
	wire, err := req.Pack()
	req.Id = origID
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest(http.MethodPost, d.url, bytes.NewReader(wire))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", dohContentType)
	httpReq.Header.Set("Accept", dohContentType)

	httpResp, err := d.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", httpResp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(httpResp.Body, 64*1024+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 64*1024 {
		return nil, errors.New("réponse DoH trop grosse")
	}

	resp := new(dns.Msg)
	if err := resp.Unpack(body); err != nil {
		return nil, fmt.Errorf("réponse DoH invalide: %w", err)
	}
	resp.Id = origID
	return resp, nil
}
