package filter

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newEngineWithLists charge deux listes de test (chacune dans un fichier) et retourne leurs identifiants.
func newEngineWithLists(t *testing.T, a, b string) (e *Engine, idA, idB string) {
	t.Helper()
	dir := t.TempDir()
	idA, idB = filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	if err := os.WriteFile(idA, []byte(a), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(idB, []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}
	e = NewEngine()
	u := NewUpdater(e, []Source{{Name: "A", URL: idA}, {Name: "B", URL: idB}}, dir, time.Hour)
	if err := u.Rebuild(); err != nil {
		t.Fatal(err)
	}
	return e, idA, idB
}

func ip(s string) net.IP { return net.ParseIP(s) }

func TestParseClient(t *testing.T) {
	for _, ok := range []string{"192.168.1.50", "192.168.1.0/24", "10.0.0.0/8", "fd00::1", "fd00::/64"} {
		if _, err := ParseClient(ok); err != nil {
			t.Errorf("%q devrait être valide: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "pas-une-ip", "192.168.1.0/99", "192.168.1.300", "1.2.3"} {
		if _, err := ParseClient(bad); err == nil {
			t.Errorf("%q devrait être refusé", bad)
		}
	}
	n, _ := ParseClient("192.168.1.50")
	if !n.Contains(ip("192.168.1.50")) || n.Contains(ip("192.168.1.51")) {
		t.Error("une adresse seule doit désigner exactement cette machine")
	}
}

func TestGroupOffDisablesBlockingForItsClients(t *testing.T) {
	e, _, _ := newEngineWithLists(t, "0.0.0.0 ads.example.com\n", "")
	e.SetUserRules(nil, []string{"pub.example.org"})
	if err := e.SetGroups([]GroupSpec{{Name: "Invités", Clients: []string{"192.168.1.50"}, Off: true}}); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"ads.example.com", "pub.example.org"} {
		if v := e.CheckClient(ip("192.168.1.50"), name); v.Blocked || v.Reason != ReasonGroupOff || v.Group != "Invités" {
			t.Errorf("%s pour le groupe sans blocage = %+v", name, v)
		}
		if v := e.CheckClient(ip("192.168.1.51"), name); !v.Blocked || v.Group != "" {
			t.Errorf("%s pour un autre client doit rester bloqué: %+v", name, v)
		}
	}
	if v := e.Check("ads.example.com"); !v.Blocked {
		t.Error("Check sans client = réglages par défaut")
	}
}

func TestGroupUsesOnlyItsLists(t *testing.T) {
	e, idA, idB := newEngineWithLists(t, "0.0.0.0 pub-a.example.com\n", "0.0.0.0 pub-b.example.com\n")
	_ = idB
	e.SetGroups([]GroupSpec{
		{Name: "Léger", Clients: []string{"192.168.1.0/24"}, Lists: []string{idA}}, // seulement la liste A
		{Name: "Tout", Clients: []string{"192.168.2.0/24"}, AllLists: true},
	})

	c := ip("192.168.1.10")
	if !e.CheckClient(c, "pub-a.example.com").Blocked {
		t.Error("la liste A est utilisée par le groupe")
	}
	if e.CheckClient(c, "pub-b.example.com").Blocked {
		t.Error("la liste B n'est PAS utilisée par ce groupe")
	}
	all := ip("192.168.2.10")
	if !e.CheckClient(all, "pub-a.example.com").Blocked || !e.CheckClient(all, "pub-b.example.com").Blocked {
		t.Error("un groupe avec toutes les listes doit bloquer les deux")
	}
	// Un client sans groupe : toutes les listes.
	if !e.CheckClient(ip("10.9.9.9"), "pub-b.example.com").Blocked {
		t.Error("sans groupe, toutes les listes s'appliquent")
	}
}

func TestGroupRulePriority(t *testing.T) {
	e, _, _ := newEngineWithLists(t, "0.0.0.0 ads.example.com\n", "")
	e.SetUserRules([]string{"global-ok.example.com"}, []string{"global-bloque.example.com"})
	e.SetGroups([]GroupSpec{{
		Name: "Enfants", Clients: []string{"192.168.1.20"}, AllLists: true,
		Allow: []string{"ads.example.com", "global-bloque.example.com"}, // l'allowlist du groupe l'emporte sur tout
		Block: []string{"jeux.example.net", "global-ok.example.com"},    // la blocklist du groupe l'emporte sur l'allowlist globale
	}})
	kid := ip("192.168.1.20")

	if v := e.CheckClient(kid, "ads.example.com"); v.Blocked || v.Reason != ReasonUserAllow || v.Group != "Enfants" {
		t.Errorf("allowlist du groupe > liste: %+v", v)
	}
	if v := e.CheckClient(kid, "global-bloque.example.com"); v.Blocked {
		t.Errorf("allowlist du groupe > blocklist globale: %+v", v)
	}
	if v := e.CheckClient(kid, "jeux.example.net"); !v.Blocked || v.Reason != ReasonUserBlock {
		t.Errorf("blocklist du groupe: %+v", v)
	}
	if v := e.CheckClient(kid, "global-ok.example.com"); !v.Blocked {
		t.Errorf("blocklist du groupe > allowlist globale: %+v", v)
	}
	// Les règles d'un groupe ne fuient pas vers les autres clients.
	if v := e.CheckClient(ip("192.168.1.21"), "jeux.example.net"); v.Blocked {
		t.Error("la blocklist du groupe ne doit pas toucher les autres clients")
	}
}

func TestFirstMatchingGroupWins(t *testing.T) {
	e, _, _ := newEngineWithLists(t, "0.0.0.0 ads.example.com\n", "")
	e.SetGroups([]GroupSpec{
		{Name: "Précis", Clients: []string{"192.168.1.50"}, Off: true},
		{Name: "Large", Clients: []string{"192.168.1.0/24"}, AllLists: true},
	})
	if v := e.CheckClient(ip("192.168.1.50"), "ads.example.com"); v.Group != "Précis" || v.Blocked {
		t.Errorf("le premier groupe qui contient le client s'applique: %+v", v)
	}
	if v := e.CheckClient(ip("192.168.1.51"), "ads.example.com"); v.Group != "Large" || !v.Blocked {
		t.Errorf("sinon le suivant: %+v", v)
	}
}

func TestSetGroupsRejectsBadInputAndKeepsPrevious(t *testing.T) {
	e, _, _ := newEngineWithLists(t, "0.0.0.0 ads.example.com\n", "")
	e.SetGroups([]GroupSpec{{Name: "Bon", Clients: []string{"192.168.1.50"}, Off: true}})

	err := e.SetGroups([]GroupSpec{{Name: "Mauvais", Clients: []string{"pas-une-ip"}}})
	if err == nil || !strings.Contains(err.Error(), "Mauvais") {
		t.Errorf("client invalide: %v", err)
	}
	if err := e.SetGroups([]GroupSpec{{Name: "X", Clients: []string{"192.168.1.1"}, Allow: []string{"pas un domaine"}}}); err == nil {
		t.Error("domaine invalide: une erreur est attendue")
	}
	if v := e.CheckClient(ip("192.168.1.50"), "ads.example.com"); v.Group != "Bon" {
		t.Error("après un refus, les groupes précédents doivent rester en place")
	}
	if err := e.SetGroups(nil); err != nil {
		t.Fatal(err)
	}
	if v := e.CheckClient(ip("192.168.1.50"), "ads.example.com"); !v.Blocked {
		t.Error("sans groupe, le client revient aux réglages par défaut")
	}
}

func TestIPv4MappedClient(t *testing.T) {
	e, _, _ := newEngineWithLists(t, "0.0.0.0 ads.example.com\n", "")
	e.SetGroups([]GroupSpec{{Name: "G", Clients: []string{"192.168.1.50"}, Off: true}})
	if v := e.CheckClient(ip("::ffff:192.168.1.50"), "ads.example.com"); v.Group != "G" {
		t.Errorf("une IPv4 présentée en IPv6 doit retrouver son groupe: %+v", v)
	}
}

func TestPauseAppliesToGroupsToo(t *testing.T) {
	e, _, _ := newEngineWithLists(t, "0.0.0.0 ads.example.com\n", "")
	e.SetGroups([]GroupSpec{{Name: "G", Clients: []string{"192.168.1.50"}, AllLists: true}})
	e.SetEnabled(false)
	if v := e.CheckClient(ip("192.168.1.50"), "ads.example.com"); v.Blocked {
		t.Error("blocage désactivé globalement : rien n'est bloqué, groupes compris")
	}
}

// --- Alias (CNAME) ---

func TestCheckAliases(t *testing.T) {
	e, _, _ := newEngineWithLists(t, "0.0.0.0 traqueur.example.net\n", "")
	client := ip("192.168.1.50")
	orig := e.CheckClient(client, "metrics.monsite.example.org") // innocent en apparence
	if orig.Blocked {
		t.Fatal("le nom demandé n'est pas dans les listes")
	}

	v, blocked := e.CheckAliases(client, orig, []string{"cdn.example.org", "collecte.traqueur.example.net"})
	if !blocked || v.Via != "collecte.traqueur.example.net" || v.Rule != "traqueur.example.net" {
		t.Errorf("l'alias vers un traqueur doit être bloqué: %+v %v", v, blocked)
	}
	if _, blocked := e.CheckAliases(client, orig, []string{"cdn.example.org", "img.example.org"}); blocked {
		t.Error("des alias innocents ne doivent rien bloquer")
	}
	if _, blocked := e.CheckAliases(client, orig, nil); blocked {
		t.Error("pas d'alias, rien à vérifier")
	}
}

func TestCheckAliasesRespectsExplicitAllow(t *testing.T) {
	e, _, _ := newEngineWithLists(t, "0.0.0.0 traqueur.example.net\n", "")
	e.SetUserRules([]string{"metrics.monsite.example.org"}, nil)
	client := ip("192.168.1.50")
	orig := e.CheckClient(client, "metrics.monsite.example.org")
	if _, blocked := e.CheckAliases(client, orig, []string{"collecte.traqueur.example.net"}); blocked {
		t.Error("un nom que tu as explicitement autorisé ne doit pas être bloqué via ses alias")
	}

	// Un alias explicitement autorisé n'est pas bloqué non plus.
	e.SetUserRules([]string{"collecte.traqueur.example.net"}, nil)
	orig = e.CheckClient(client, "autre.example.org")
	if _, blocked := e.CheckAliases(client, orig, []string{"collecte.traqueur.example.net"}); blocked {
		t.Error("une cible autorisée par ton allowlist ne doit pas être bloquée")
	}

	// Groupe sans blocage : jamais.
	e.SetUserRules(nil, nil)
	e.SetGroups([]GroupSpec{{Name: "Libre", Clients: []string{"192.168.1.60"}, Off: true}})
	free := ip("192.168.1.60")
	if _, blocked := e.CheckAliases(free, e.CheckClient(free, "x.example.org"), []string{"collecte.traqueur.example.net"}); blocked {
		t.Error("un groupe sans blocage ne doit rien bloquer")
	}
}

func TestCheckAliasesCanBeDisabled(t *testing.T) {
	e, _, _ := newEngineWithLists(t, "0.0.0.0 traqueur.example.net\n", "")
	client := ip("192.168.1.50")
	orig := e.CheckClient(client, "metrics.monsite.example.org")
	e.SetCNAMECheck(false)
	if _, blocked := e.CheckAliases(client, orig, []string{"collecte.traqueur.example.net"}); blocked {
		t.Error("vérification désactivée : rien ne doit être bloqué")
	}
	e.SetCNAMECheck(true)
	if _, blocked := e.CheckAliases(client, orig, []string{"collecte.traqueur.example.net"}); !blocked {
		t.Error("réactivée : doit bloquer")
	}
}

func TestCountsSumsAllLists(t *testing.T) {
	e, _, _ := newEngineWithLists(t, "0.0.0.0 a.example.com\n0.0.0.0 b.example.com\n@@||ok.example.com^\n", "0.0.0.0 c.example.com\n")
	if b, a := e.Counts(); b != 3 || a != 1 {
		t.Errorf("Counts = %d bloqués, %d exceptions; attendu 3 et 1", b, a)
	}
}
