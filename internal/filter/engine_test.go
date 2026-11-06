package filter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnginePriority(t *testing.T) {
	e := NewEngine()
	e.SetUserRules(
		[]string{"allowed.example.com"},
		[]string{"mine.example.org", "allowed.example.com"}, // en conflit : l'allowlist gagne
	)

	dir := t.TempDir()
	list := filepath.Join(dir, "list.txt")
	content := "0.0.0.0 ads.example.net\n0.0.0.0 shared.example.net\n@@||shared.example.net^\n"
	if err := os.WriteFile(list, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	u := NewUpdater(e, []Source{{Name: "test", URL: list}}, dir, time.Hour)
	if err := u.Rebuild(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		blocked bool
		reason  Reason
	}{
		{"ads.example.net.", true, ReasonList},          // point final DNS + liste
		{"SUB.ADS.example.net", true, ReasonList},       // casse + sous-domaine
		{"mine.example.org", true, ReasonUserBlock},     // blocklist perso
		{"allowed.example.com", false, ReasonUserAllow}, // allowlist > blocklist
		{"shared.example.net", false, ReasonListAllow},  // exception de liste
		{"unknown.example.io", false, ReasonNone},
	}
	for _, tt := range tests {
		v := e.Check(tt.name)
		if v.Blocked != tt.blocked || v.Reason != tt.reason {
			t.Errorf("Check(%q) = %+v, attendu blocked=%v reason=%v", tt.name, v, tt.blocked, tt.reason)
		}
	}

	e.SetEnabled(false)
	if v := e.Check("ads.example.net"); v.Blocked {
		t.Error("le blocage désactivé ne doit rien bloquer")
	}
}

// Une URL injoignable ne doit pas vider un cache déjà présent.
func TestUpdaterKeepsCacheOnFailure(t *testing.T) {
	e := NewEngine()
	dir := t.TempDir()
	src := Source{Name: "down", URL: "http://127.0.0.1:1/list.txt"} // port fermé
	u := NewUpdater(e, []Source{src}, dir, time.Hour)

	// On simule une liste déjà téléchargée lors d'un démarrage précédent.
	if err := os.WriteFile(u.cachePath(src), []byte("0.0.0.0 cached.example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := u.Update(ctx); err == nil {
		t.Error("une erreur de téléchargement était attendue")
	}
	if !e.Check("cached.example.com").Blocked {
		t.Error("la copie en cache aurait dû rester active")
	}
}

func TestPauseResumesByItself(t *testing.T) {
	e := NewEngine()
	e.SetUserRules(nil, []string{"ads.example.com"})
	if !e.Check("ads.example.com").Blocked {
		t.Fatal("devrait être bloqué avant la pause")
	}

	e.Pause(80 * time.Millisecond)
	if e.Check("ads.example.com").Blocked || e.Enabled() {
		t.Error("pendant la pause, rien ne doit être bloqué")
	}
	if _, until := e.State(); until.IsZero() {
		t.Error("la fin de pause devrait être indiquée")
	}

	time.Sleep(120 * time.Millisecond)
	if !e.Check("ads.example.com").Blocked {
		t.Error("le blocage doit reprendre à la fin de la pause")
	}

	e.Pause(time.Hour)
	e.Resume() // reprise manuelle avant l'échéance
	if !e.Check("ads.example.com").Blocked {
		t.Error("Resume doit réactiver le blocage")
	}
}

// On colle souvent une adresse complète dans le champ « domaine » : seul le nom compte.
func TestDomainFromInput(t *testing.T) {
	ok := map[string]string{
		"exemple.com":                             "exemple.com",
		"  Pub.Exemple.COM  ":                     "pub.exemple.com",
		"https://www.youtube.com/watch?v=abc":     "www.youtube.com",
		"http://ads.exemple.com:8080/banniere.js": "ads.exemple.com",
		"exemple.com/pub/":                        "exemple.com",
		"exemple.com:443":                         "exemple.com",
		"*.traqueur.net":                          "traqueur.net",
		"https://user:mdp@pub.exemple.com/x":      "pub.exemple.com",
		"exemple.com.":                            "exemple.com",
		"https://exemple.com#ancre":               "exemple.com",
	}
	for in, want := range ok {
		if got, valid := DomainFromInput(in); !valid || got != want {
			t.Errorf("%q : %q %v, attendu %q", in, got, valid, want)
		}
	}
	for _, in := range []string{"", "https://", "com", "192.168.1.1", "http://192.168.1.1/x", "pas un domaine", "[::1]:53"} {
		if got, valid := DomainFromInput(in); valid {
			t.Errorf("%q ne doit pas être accepté (obtenu %q)", in, got)
		}
	}
}
