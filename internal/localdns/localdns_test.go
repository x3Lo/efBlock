package localdns

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func ask(z *Zone, name string, qtype uint16) (*dns.Msg, bool) {
	return z.Answer(new(dns.Msg).SetQuestion(name, qtype))
}

func mustZone(t *testing.T, upstreamAddr string, domains, hosts []string) *Zone {
	t.Helper()
	z, err := New(upstreamAddr, domains, hosts, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	return z
}

func TestStaticHosts(t *testing.T) {
	z := mustZone(t, "", nil, []string{
		"192.168.1.20 nas.maison nas  # le serveur de fichiers",
		"fd00::20 nas.maison",
		"192.168.1.1 box.maison",
		"",
		"# ligne de commentaire",
	})

	r, ok := ask(z, "NAS.maison.", dns.TypeA)
	if !ok || r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != "192.168.1.20" || !r.Authoritative {
		t.Fatalf("A nas.maison = %+v", r)
	}
	r, _ = ask(z, "nas.maison.", dns.TypeAAAA)
	if len(r.Answer) != 1 || r.Answer[0].(*dns.AAAA).AAAA.String() != "fd00::20" {
		t.Errorf("AAAA nas.maison = %+v", r.Answer)
	}
	// Nom connu, mais pas de ce type : NOERROR vide ("NODATA"), pas NXDOMAIN.
	r, ok = ask(z, "box.maison.", dns.TypeAAAA)
	if !ok || r.Rcode != dns.RcodeSuccess || len(r.Answer) != 0 {
		t.Errorf("AAAA sans adresse v6 : rcode=%d réponses=%d, attendu NOERROR vide", r.Rcode, len(r.Answer))
	}
	// Un nom court défini explicitement répond.
	if r, ok := ask(z, "nas.", dns.TypeA); !ok || len(r.Answer) != 1 {
		t.Errorf("nom court \"nas\" : %+v", r)
	}
}

func TestReverseLookupOfStaticHosts(t *testing.T) {
	z := mustZone(t, "", nil, []string{"192.168.1.20 nas.maison", "192.168.1.20 autre-nom.maison"})
	r, ok := ask(z, "20.1.168.192.in-addr.arpa.", dns.TypePTR)
	if !ok || len(r.Answer) != 1 || r.Answer[0].(*dns.PTR).Ptr != "nas.maison." {
		t.Errorf("PTR = %+v (le premier nom déclaré doit être le nom officiel)", r)
	}
}

// Le bug observé en vrai : la recherche inverse d'une IP privée partait chez 1.1.1.1.
func TestPrivateReverseNeverLeaks(t *testing.T) {
	z := mustZone(t, "", nil, nil) // aucun résolveur local configuré
	for _, name := range []string{
		"10.50.168.192.in-addr.arpa.", "5.0.0.10.in-addr.arpa.", "1.20.172.in-addr.arpa.", "1.0.254.169.in-addr.arpa.",
		"1.0.0.127.in-addr.arpa.", "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.ip6.arpa.",
	} {
		r, ok := ask(z, name, dns.TypePTR)
		if !ok || r.Rcode != dns.RcodeNameError {
			t.Errorf("%s : handled=%v, attendu une réponse NXDOMAIN locale", name, ok)
		}
	}
	// Une zone inverse PUBLIQUE, elle, doit continuer vers internet.
	for _, name := range []string{"8.8.8.8.in-addr.arpa.", "1.1.1.1.in-addr.arpa.", "1.32.172.in-addr.arpa.", "1.15.172.in-addr.arpa."} {
		if _, ok := ask(z, name, dns.TypePTR); ok {
			t.Errorf("%s est publique : ne doit pas être traitée en local (172.16/12 s'arrête à 172.31)", name)
		}
	}
}

func TestSpecialDomainsStayLocal(t *testing.T) {
	z := mustZone(t, "", nil, nil)
	for _, name := range []string{"imprimante.local.", "localhost.", "routeur.home.arpa."} {
		if r, ok := ask(z, name, dns.TypeA); !ok || r.Rcode != dns.RcodeNameError {
			t.Errorf("%s doit recevoir NXDOMAIN en local", name)
		}
	}
	if _, ok := ask(z, "example.com.", dns.TypeA); ok {
		t.Error("un nom public n'est pas local")
	}
	if _, ok := ask(z, "notlocal.com.", dns.TypeA); ok {
		t.Error("\"notlocal.com\" ne finit pas par le domaine \"local\"")
	}
}

// Un résolveur local (la box) reçoit les domaines locaux et seulement eux.
func TestLocalDomainsGoToLocalResolver(t *testing.T) {
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	addr := pc.LocalAddr().String()
	pc.Close()
	var got []string
	started := make(chan struct{})
	srv := &dns.Server{Addr: addr, Net: "udp", NotifyStartedFunc: func() { close(started) },
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
			got = append(got, r.Question[0].Name)
			resp := new(dns.Msg)
			resp.SetReply(r)
			if r.Question[0].Qtype == dns.TypePTR {
				resp.Answer = []dns.RR{&dns.PTR{Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: 30}, Ptr: "pc-salon.maison."}}
			} else {
				resp.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30}, A: net.ParseIP("192.168.1.77")}}
			}
			_ = w.WriteMsg(resp)
		})}
	go srv.ListenAndServe()
	<-started
	defer srv.Shutdown()

	z := mustZone(t, addr, []string{"maison", "lan"}, nil)

	r, ok := ask(z, "imprimante.maison.", dns.TypeA)
	if !ok || len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != "192.168.1.77" {
		t.Fatalf("imprimante.maison = %+v", r)
	}
	if _, ok := ask(z, "tv.sous.lan.", dns.TypeA); !ok {
		t.Error("les sous-domaines d'un domaine local sont locaux")
	}
	if _, ok := ask(z, "example.com.", dns.TypeA); ok {
		t.Error("un nom public ne doit pas aller au résolveur local")
	}
	if r, ok := ask(z, "99.1.168.192.in-addr.arpa.", dns.TypePTR); !ok || len(r.Answer) != 1 {
		t.Errorf("la recherche inverse privée doit être confiée au résolveur local: %+v", r)
	}

	// Nom du client (recherche inverse en arrière-plan, puis résultat en cache).
	if n := z.ClientName("192.168.1.99"); n != "" {
		t.Logf("le 1er appel peut déjà répondre (%q), sinon vide", n)
	}
	deadline := time.Now().Add(2 * time.Second)
	var name string
	for time.Now().Before(deadline) {
		if name = z.ClientName("192.168.1.99"); name != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if name != "pc-salon.maison" {
		t.Errorf("nom du client = %q, attendu pc-salon.maison", name)
	}
}

func TestConfigErrors(t *testing.T) {
	tests := []struct {
		name     string
		upstream string
		domains  []string
		hosts    []string
		want     string
	}{
		{"domaines sans résolveur", "", []string{"maison"}, nil, "sans résolveur"},
		{"hôte sans nom", "", nil, []string{"192.168.1.20"}, "attendu"},
		{"IP invalide", "", nil, []string{"pas-une-ip nas.maison"}, "attendu"},
		{"nom invalide", "", nil, []string{"192.168.1.20 na$.maison"}, "invalide"},
		{"domaine invalide", "192.168.1.1", []string{"mai son"}, nil, "invalide"},
	}
	for _, tt := range tests {
		_, err := New(tt.upstream, tt.domains, tt.hosts, time.Second)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s : erreur = %v, attendu %q", tt.name, err, tt.want)
		}
	}
}

func TestNilZoneIsHarmless(t *testing.T) {
	var z *Zone
	if _, ok := z.Answer(new(dns.Msg).SetQuestion("example.com.", dns.TypeA)); ok {
		t.Error("une zone nil ne doit rien traiter")
	}
	if z.ClientName("192.168.1.2") != "" {
		t.Error("une zone nil ne connaît aucun nom")
	}
}
