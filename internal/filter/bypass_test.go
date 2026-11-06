package filter

import (
	"net"
	"testing"
	"time"
)

func TestBypassBlocked(t *testing.T) {
	e := NewEngine()
	for _, name := range []string{
		"use-application-dns.net.",    // domaine témoin de Firefox
		"mask.icloud.com.",            // iCloud Private Relay
		"mozilla.cloudflare-dns.com.", // sous-domaine d'un serveur DoH connu
		"dns.google",                  // DNS privé d'Android
	} {
		v := e.Check(name)
		if !v.Blocked || v.Reason != ReasonBypass || !v.NXDomain {
			t.Errorf("%s : %+v, attendu bloqué en NXDOMAIN (contournement)", name, v)
		}
	}
	// Des noms proches, mais légitimes, ne sont pas touchés.
	for _, name := range []string{"icloud.com", "www.google.com", "google.com", "cloudflare.com"} {
		if v := e.Check(name); v.Blocked {
			t.Errorf("%s ne doit pas être bloqué : %+v", name, v)
		}
	}
}

func TestBypassPriorities(t *testing.T) {
	e := NewEngine()

	// Désactivé : plus rien n'est bloqué à ce titre.
	e.SetBypassBlock(false)
	if v := e.Check("use-application-dns.net"); v.Blocked {
		t.Errorf("option désactivée : %+v", v)
	}
	e.SetBypassBlock(true)

	// Une autorisation de l'utilisateur l'emporte (ex : il veut garder dns.google).
	e.SetUserRules([]string{"dns.google"}, nil)
	if v := e.Check("dns.google"); v.Blocked {
		t.Errorf("domaine autorisé par l'utilisateur : %+v", v)
	}

	// Blocage en pause ou coupé : rien n'est bloqué, contournements compris.
	e.Pause(time.Minute)
	if v := e.Check("use-application-dns.net"); v.Blocked {
		t.Errorf("blocage en pause : %+v", v)
	}
	e.Resume()

	// Un groupe sans blocage n'est pas concerné.
	if err := e.SetGroups([]GroupSpec{{Name: "libre", Clients: []string{"192.168.1.50"}, Off: true}}); err != nil {
		t.Fatal(err)
	}
	if v := e.CheckClient(net.ParseIP("192.168.1.50"), "use-application-dns.net"); v.Blocked {
		t.Errorf("groupe sans blocage : %+v", v)
	}
	if v := e.CheckClient(net.ParseIP("192.168.1.51"), "use-application-dns.net"); !v.Blocked {
		t.Errorf("hors du groupe : le contournement doit être bloqué : %+v", v)
	}
}
