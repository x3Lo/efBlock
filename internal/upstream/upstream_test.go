package upstream

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// answer fabrique la réponse du faux serveur : 1.2.3.4 pour tout.
func answer(req *dns.Msg) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Answer = []dns.RR{&dns.A{
		Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
		A:   net.ParseIP("1.2.3.4"),
	}}
	return resp
}

func checkAnswer(t *testing.T, resp *dns.Msg, id uint16) {
	t.Helper()
	if resp.Id != id {
		t.Errorf("ID = %d, attendu %d", resp.Id, id)
	}
	if len(resp.Answer) != 1 || resp.Answer[0].(*dns.A).A.String() != "1.2.3.4" {
		t.Errorf("réponse inattendue: %v", resp.Answer)
	}
}

// selfSigned génère un certificat valable pour 127.0.0.1 et le pool qui lui fait confiance.
func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func TestDoT(t *testing.T) {
	cert, pool := selfSigned(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := "127.0.0.1:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()

	started := make(chan struct{})
	srv := &dns.Server{
		Addr: addr, Net: "tcp-tls",
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}},
		NotifyStartedFunc: func() { close(started) },
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			_ = w.WriteMsg(answer(req))
		}),
	}
	go srv.ListenAndServe()
	<-started
	defer srv.Shutdown()

	u := newDoT(addr, &tls.Config{RootCAs: pool}, &net.Dialer{Timeout: 2 * time.Second}, 2*time.Second)

	// Plusieurs requêtes d'affilée : les suivantes réutilisent la connexion ouverte.
	for i := 1; i <= 3; i++ {
		req := new(dns.Msg).SetQuestion("example.com.", dns.TypeA)
		req.Id = uint16(100 + i)
		resp, err := u.Resolve(req)
		if err != nil {
			t.Fatalf("requête %d: %v", i, err)
		}
		checkAnswer(t, resp, req.Id)
	}
	if len(u.idle) != 1 {
		t.Errorf("%d connexion(s) gardée(s) au repos, attendu 1 (réutilisation)", len(u.idle))
	}
}

// Sans faire confiance au certificat, la connexion doit être REFUSÉE :
// c'est la protection contre quelqu'un qui se ferait passer pour le serveur.
func TestDoTRejectsUntrustedCertificate(t *testing.T) {
	cert, _ := selfSigned(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := "127.0.0.1:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()
	started := make(chan struct{})
	srv := &dns.Server{Addr: addr, Net: "tcp-tls",
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}},
		NotifyStartedFunc: func() { close(started) },
		Handler:           dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) { _ = w.WriteMsg(answer(r)) })}
	go srv.ListenAndServe()
	<-started
	defer srv.Shutdown()

	u := newDoT(addr, nil, &net.Dialer{Timeout: time.Second}, time.Second) // config TLS par défaut : ne connaît pas ce certificat
	if _, err := u.Resolve(new(dns.Msg).SetQuestion("example.com.", dns.TypeA)); err == nil {
		t.Fatal("un certificat inconnu aurait dû être refusé")
	}
}

func TestDoH(t *testing.T) {
	var gotID uint16 = 999
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != dohContentType {
			http.Error(w, "mauvaise requête", http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		req := new(dns.Msg)
		if err := req.Unpack(body); err != nil {
			http.Error(w, "dns invalide", http.StatusBadRequest)
			return
		}
		gotID = req.Id
		wire, _ := answer(req).Pack()
		w.Header().Set("Content-Type", dohContentType)
		_, _ = w.Write(wire)
	}))
	defer srv.Close()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	u := newDoH(srv.URL, &tls.Config{RootCAs: pool}, &net.Dialer{Timeout: 2 * time.Second}, 2*time.Second)

	req := new(dns.Msg).SetQuestion("example.com.", dns.TypeA)
	req.Id = 4242
	resp, err := u.Resolve(req)
	if err != nil {
		t.Fatal(err)
	}
	checkAnswer(t, resp, 4242)
	if gotID != 0 {
		t.Errorf("l'ID envoyé en DoH était %d, la RFC 8484 recommande 0", gotID)
	}
	if req.Id != 4242 {
		t.Error("la requête d'origine ne doit pas être modifiée")
	}
}

func TestDoHHTTPError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "panne", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	u := newDoH(srv.URL, &tls.Config{RootCAs: pool}, &net.Dialer{}, 2*time.Second)
	if _, err := u.Resolve(new(dns.Msg).SetQuestion("example.com.", dns.TypeA)); err == nil {
		t.Fatal("une erreur HTTP 503 doit remonter")
	}
}

func TestNewResolverKinds(t *testing.T) {
	tests := []struct {
		spec     string
		wantName string
		wantErr  bool
	}{
		{"1.1.1.1", "1.1.1.1:53", false},
		{"9.9.9.9:5353", "9.9.9.9:5353", false},
		{"2606:4700:4700::1111", "[2606:4700:4700::1111]:53", false},
		{"tls://1.1.1.1", "tls://1.1.1.1:853", false},
		{"tls://dns.quad9.net:8853", "tls://dns.quad9.net:8853", false},
		{"https://cloudflare-dns.com/dns-query", "https://cloudflare-dns.com/dns-query", false},
		{"https://", "", true},
		{"quic://1.1.1.1", "", true},
	}
	for _, tt := range tests {
		r, err := newResolver(tt.spec, time.Second, &net.Dialer{})
		if (err != nil) != tt.wantErr {
			t.Errorf("%q: erreur = %v, attendu erreur=%v", tt.spec, err, tt.wantErr)
			continue
		}
		if err == nil && r.Name() != tt.wantName {
			t.Errorf("%q: nom = %q, attendu %q", tt.spec, r.Name(), tt.wantName)
		}
	}
}

// Le premier upstream est en panne : le suivant doit prendre le relais.
func TestForwarderFailover(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	good := pc.LocalAddr().String()
	pc.Close()
	srv := &dns.Server{Addr: good, Net: "udp",
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) { _ = w.WriteMsg(answer(r)) })}
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	go srv.ListenAndServe()
	<-started
	defer srv.Shutdown()

	f, err := New([]string{"127.0.0.1:1", good}, 500*time.Millisecond, nil) // le port 1 est fermé
	if err != nil {
		t.Fatal(err)
	}
	req := new(dns.Msg).SetQuestion("example.com.", dns.TypeA)
	resp, err := f.Resolve(req)
	if err != nil {
		t.Fatalf("le basculement aurait dû réussir: %v", err)
	}
	checkAnswer(t, resp, req.Id)

	if _, err := New(nil, time.Second, nil); err == nil {
		t.Error("New sans upstream doit échouer")
	}
}
