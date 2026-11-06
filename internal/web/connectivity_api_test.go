package web

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/x3Lo/efBlock/internal/upstream"
)

// fakeUpstream : un résolveur local qui répond à toute question.
func fakeUpstream(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = append(m.Answer, &dns.NS{Hdr: dns.RR_Header{Name: ".", Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: 60}, Ns: "a.root-servers.net."})
		_ = w.WriteMsg(m)
	})}
	go srv.ActivateAndServe()
	t.Cleanup(func() { srv.Shutdown() })
	return pc.LocalAddr().String()
}

func connectivity(t *testing.T, e *env) connectivityInfo {
	t.Helper()
	c := e.login(t)
	rec := e.do("GET", "/api/connectivity", "192.168.1.5:1111", c, nil, false)
	if rec.Code != 200 {
		t.Fatalf("GET /api/connectivity : %d %s", rec.Code, rec.Body)
	}
	var info connectivityInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	return info
}

func TestConnectivityRequiresLogin(t *testing.T) {
	e := newEnv(t)
	if rec := e.do("GET", "/api/connectivity", "192.168.1.5:1111", nil, nil, false); rec.Code != 401 {
		t.Errorf("sans connexion : %d, attendu 401", rec.Code)
	}
}

func TestConnectivityOK(t *testing.T) {
	f, err := upstream.New([]string{fakeUpstream(t)}, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t)
	e.srv.d.Upstream = func() *upstream.Forwarder { return f }
	info := connectivity(t, e)
	if !info.DNS || info.Error != "" || info.Upstream != 1 || info.Checked == "" {
		t.Errorf("accès attendu : %+v", info)
	}
	if info.Updates != "unknown" {
		t.Errorf("GitHub jamais interrogé : état %q, attendu unknown", info.Updates)
	}
}

func TestConnectivityDown(t *testing.T) {
	f, err := upstream.New([]string{"127.0.0.1:1"}, 300*time.Millisecond, nil) // port fermé
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t)
	e.srv.d.Upstream = func() *upstream.Forwarder { return f }
	if info := connectivity(t, e); info.DNS || info.Error == "" {
		t.Errorf("aucun résolveur joignable : %+v", info)
	}
}

func TestConnectivityWithoutUpstream(t *testing.T) {
	e := newEnv(t) // pas de résolveur amont fourni
	if info := connectivity(t, e); info.DNS || info.Error == "" {
		t.Errorf("sans résolveur : %+v", info)
	}
}
