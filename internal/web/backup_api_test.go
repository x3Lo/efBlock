package web

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBackupRestoreRoundTrip(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	const ip = "192.168.1.5:1111"
	must := func(method, path string, body any) {
		t.Helper()
		if rec := e.do(method, path, ip, c, body, true); rec.Code >= 300 {
			t.Fatalf("%s %s : %d %s", method, path, rec.Code, rec.Body)
		}
	}
	// Une configuration faite depuis l'interface.
	must("POST", "/api/blocklist", map[string]string{"domain": "pub.example.com"})
	must("POST", "/api/allowlist", map[string]string{"domain": "ok.example.com"})
	must("POST", "/api/lists", map[string]string{"name": "Ma liste", "url": "https://listes.example.com/hosts.txt"})
	must("PUT", "/api/settings/filter", map[string]any{"block_mode": "nxdomain", "update_interval": "12h"})

	rec := e.do("GET", "/api/backup", ip, c, nil, false)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), "efblock-sauvegarde-") {
		t.Fatalf("sauvegarde : %d, en-tête %q", rec.Code, rec.Header().Get("Content-Disposition"))
	}
	backup := rec.Body.Bytes()
	if strings.Contains(string(backup), "password_hash") || strings.Contains(string(backup), "$2a$") {
		t.Error("la sauvegarde ne doit pas contenir le mot de passe")
	}

	// On défait tout...
	must("DELETE", "/api/blocklist?domain=pub.example.com", nil)
	must("DELETE", "/api/allowlist?domain=ok.example.com", nil)
	must("DELETE", "/api/lists?url="+"https%3A%2F%2Flistes.example.com%2Fhosts.txt", nil)
	must("DELETE", "/api/settings/filter", nil)
	if v := e.engine.Check("pub.example.com"); v.Blocked {
		t.Fatal("préparation : le domaine ne devrait plus être bloqué")
	}

	// ... puis on restaure.
	var body any
	if err := json.Unmarshal(backup, &body); err != nil {
		t.Fatal(err)
	}
	if rec := e.do("POST", "/api/restore", ip, c, body, true); rec.Code != 200 {
		t.Fatalf("restauration : %d %s", rec.Code, rec.Body)
	}
	if v := e.engine.Check("pub.example.com"); !v.Blocked {
		t.Error("après restauration, pub.example.com doit être bloqué")
	}
	if v := e.engine.Check("ok.example.com"); v.Blocked {
		t.Error("après restauration, ok.example.com doit être autorisé")
	}
	if e.applied == nil || e.applied.Filter.BlockMode != "nxdomain" {
		t.Error("après restauration, le mode de blocage doit être nxdomain")
	}
	if rec := e.do("GET", "/api/lists", ip, c, nil, false); !strings.Contains(rec.Body.String(), "listes.example.com") {
		t.Error("après restauration, la liste doit être revenue")
	}
	// Le mot de passe n'a pas changé : on peut toujours se connecter.
	e.login(t)
}

func TestRestoreRejectsBadFiles(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	const ip = "192.168.1.5:1111"
	if rec := e.do("POST", "/api/blocklist", ip, c, map[string]string{"domain": "garde.example.com"}, true); rec.Code >= 300 {
		t.Fatal(rec.Body)
	}
	cases := map[string]any{
		"pas une sauvegarde": map[string]any{"bonjour": 1},
		"format inconnu":     map[string]any{"format": "autre-chose", "version": 1},
		"version future":     map[string]any{"format": backupFormat, "version": 99},
		"domaine invalide":   map[string]any{"format": backupFormat, "version": 1, "state": map[string]any{"blocklist": []string{"pas un domaine"}}},
		"liste invalide":     map[string]any{"format": backupFormat, "version": 1, "state": map[string]any{"lists": []map[string]string{{"name": "x", "url": "ftp://x"}}}},
		"réglage invalide":   map[string]any{"format": backupFormat, "version": 1, "settings": map[string]any{"filter": map[string]string{"block_mode": "zero_ip", "update_interval": "jamais"}}},
	}
	for name, body := range cases {
		if rec := e.do("POST", "/api/restore", ip, c, body, true); rec.Code != 400 {
			t.Errorf("%s : %d %s, attendu 400", name, rec.Code, rec.Body)
		}
		// Rien n'a changé.
		if v := e.engine.Check("garde.example.com"); !v.Blocked {
			t.Fatalf("%s : la configuration existante a été modifiée", name)
		}
	}
	// Sans en-tête anti-CSRF : refusé.
	if rec := e.do("POST", "/api/restore", ip, c, map[string]any{"format": backupFormat, "version": 1}, false); rec.Code != 403 {
		t.Errorf("sans en-tête anti-CSRF : %d, attendu 403", rec.Code)
	}
}
