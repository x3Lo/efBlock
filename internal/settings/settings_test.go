package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/x3Lo/efBlock/internal/config"
)

func baseConfig(t *testing.T) *config.Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "dns:\n  upstreams: [\"tls://1.1.1.1\"]\n  timeout: 3s\nfilter:\n  update_interval: 24h\nweb:\n  password_hash: \"hash-config\"\n"
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func TestUpdateAppliesPersistsAndOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	base := baseConfig(t)
	m, err := Open(path, base)
	if err != nil {
		t.Fatal(err)
	}
	var applied *config.Config
	m.SetApply(func(c *config.Config) error { applied = c; return nil })

	err = m.Update(GroupUpstream, raw(Upstream{Upstreams: []string{"tls://9.9.9.9", "https://dns.quad9.net/dns-query"}, Bootstrap: []string{"9.9.9.9:53"}, Timeout: "5s"}))
	if err != nil {
		t.Fatal(err)
	}
	if applied == nil || len(applied.DNS.Upstreams) != 2 || applied.DNS.Timeout != 5*time.Second {
		t.Fatalf("le changement doit être mis en application: %+v", applied)
	}
	if got := m.Effective().DNS.Upstreams[0]; got != "tls://9.9.9.9" {
		t.Errorf("effectif = %q", got)
	}
	if base.DNS.Upstreams[0] != "tls://1.1.1.1" {
		t.Error("la configuration de base ne doit JAMAIS être modifiée")
	}

	// Redémarrage : les réglages reviennent depuis le fichier.
	m2, err := Open(path, baseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := m2.Effective().DNS.Upstreams; len(got) != 2 || got[1] != "https://dns.quad9.net/dns-query" {
		t.Errorf("après redémarrage: %v", got)
	}
	if v := m2.View(); len(v.Overridden) != 1 || v.Overridden[0] != GroupUpstream {
		t.Errorf("groupes modifiés = %v", v.Overridden)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("settings.json doit être en 0600, il est en %v", fi.Mode().Perm())
	}
}

func TestResetRestoresConfigYaml(t *testing.T) {
	m, _ := Open(filepath.Join(t.TempDir(), "s.json"), baseConfig(t))
	m.Update(GroupFilter, raw(Filter{BlockMode: "nxdomain", UpdateInterval: "6h"}))
	if f := m.Effective().Filter; f.BlockMode != "nxdomain" || f.UpdateInterval != 6*time.Hour {
		t.Fatalf("filter = %+v", f)
	}
	if err := m.Reset(GroupFilter); err != nil {
		t.Fatal(err)
	}
	if f := m.Effective().Filter; f.BlockMode != "zero_ip" || f.UpdateInterval != 24*time.Hour {
		t.Errorf("après rétablissement: %+v", f)
	}
	if len(m.View().Overridden) != 0 {
		t.Error("plus aucun groupe ne doit être marqué comme modifié")
	}
}

func TestInvalidValuesAreRejectedAndNothingChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	m, _ := Open(path, baseConfig(t))
	calls := 0
	m.SetApply(func(*config.Config) error { calls++; return nil })

	tests := []struct {
		name  string
		group string
		body  any
		want  string
	}{
		{"mode de blocage inconnu", GroupFilter, Filter{BlockMode: "boum", UpdateInterval: "24h"}, "block_mode"},
		{"durée invalide", GroupFilter, Filter{BlockMode: "zero_ip", UpdateInterval: "demain"}, "update_interval"},
		{"intervalle trop court", GroupFilter, Filter{BlockMode: "zero_ip", UpdateInterval: "5s"}, "update_interval"},
		{"plus aucun upstream", GroupUpstream, Upstream{Upstreams: nil, Timeout: "3s"}, "upstream"},
		{"réseau invalide", GroupAccess, Access{DNSClients: []string{"pas-un-reseau"}}, "dns.allowed_clients"},
		{"domaines locaux sans résolveur", GroupLocal, Local{Domains: []string{"maison"}}, "dns.local.upstream"},
		{"cache négatif", GroupCache, Cache{Enabled: true, MaxEntries: -3, MaxTTL: "1h"}, "max_entries"},
		{"champ inconnu", GroupFilter, map[string]any{"block_mode": "zero_ip", "update_interval": "24h", "surprise": 1}, "unknown field"},
		{"groupe inconnu", "nimportequoi", struct{}{}, "groupe inconnu"},
	}
	for _, tt := range tests {
		err := m.Update(tt.group, raw(tt.body))
		if err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s : erreur = %v, attendu ErrInvalid contenant %q", tt.name, err, tt.want)
		}
	}
	if calls != 0 {
		t.Errorf("la mise en application ne doit pas être appelée pour des valeurs invalides (%d appels)", calls)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("aucun fichier ne doit être écrit")
	}
}

// Si la mise en application échoue (ex. upstream impossible à construire), on n'enregistre rien.
func TestApplyFailureRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	m, _ := Open(path, baseConfig(t))
	m.SetApply(func(*config.Config) error { return errors.New("upstream \"ftp://x\": protocole non géré") })

	err := m.Update(GroupUpstream, raw(Upstream{Upstreams: []string{"ftp://x"}, Timeout: "3s"}))
	if err == nil || !strings.Contains(err.Error(), "protocole non géré") {
		t.Fatalf("erreur = %v", err)
	}
	if m.Effective().DNS.Upstreams[0] != "tls://1.1.1.1" {
		t.Error("la configuration effective ne doit pas changer")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("rien ne doit être sauvegardé")
	}
}

func TestPasswordOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	m, _ := Open(path, baseConfig(t))
	if m.Effective().Web.PasswordHash != "hash-config" {
		t.Fatal("le hash de config.yaml est la valeur de départ")
	}
	if err := m.SetPasswordHash("hash-ui"); err != nil {
		t.Fatal(err)
	}
	m2, _ := Open(path, baseConfig(t))
	if m2.Effective().Web.PasswordHash != "hash-ui" {
		t.Error("le mot de passe changé depuis l'interface doit survivre au redémarrage")
	}
}

func TestCorruptSettingsFileIsIgnoredNotFatal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(path, []byte("{ ceci n'est pas du JSON"), 0o600)
	m, err := Open(path, baseConfig(t))
	if err != nil {
		t.Fatalf("un fichier abîmé ne doit pas empêcher le démarrage: %v", err)
	}
	if m.Effective().DNS.Upstreams[0] != "tls://1.1.1.1" {
		t.Error("retour aux valeurs de config.yaml attendu")
	}
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Error("le fichier abîmé doit être conservé en .bad pour analyse")
	}

	// Un fichier lisible mais aux valeurs invalides est traité de la même façon.
	path2 := filepath.Join(t.TempDir(), "s2.json")
	os.WriteFile(path2, []byte(`{"filter":{"block_mode":"inconnu","update_interval":"24h"}}`), 0o600)
	if _, err := Open(path2, baseConfig(t)); err != nil {
		t.Errorf("valeurs invalides: %v", err)
	}
}

func TestViewListsInfraAndFormatsDurations(t *testing.T) {
	m, _ := Open(filepath.Join(t.TempDir(), "s.json"), baseConfig(t))
	v := m.View()
	if v.Effective.Filter.UpdateInterval != "24h" || v.Effective.Upstream.Timeout != "3s" {
		t.Errorf("durées lisibles attendues: %+v", v.Effective)
	}
	if v.Infra.DNSListen != ":53" || v.Infra.WebListen != ":8080" {
		t.Errorf("infra = %+v", v.Infra)
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "null") {
		t.Errorf("le JSON ne doit pas contenir de null (listes vides = []): %s", b)
	}
}

func TestNewOptionsDefaultsAndRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	m, _ := Open(path, baseConfig(t))
	v := m.View().Effective
	if v.Filter.CNAMECheck == nil || !*v.Filter.CNAMECheck {
		t.Error("la vérification des alias est active par défaut")
	}
	if v.Cache.ServeStale == nil || !*v.Cache.ServeStale || v.Cache.StaleMax != "24h" || v.Cache.Prefetch == nil || !*v.Cache.Prefetch {
		t.Errorf("valeurs par défaut du cache: %+v", v.Cache)
	}

	off := false
	if err := m.Update(GroupFilter, raw(Filter{BlockMode: "zero_ip", UpdateInterval: "24h", CNAMECheck: &off})); err != nil {
		t.Fatal(err)
	}
	if err := m.Update(GroupCache, raw(Cache{Enabled: true, MaxEntries: 500, MaxTTL: "1h", ServeStale: &off, StaleMax: "6h", Prefetch: &off})); err != nil {
		t.Fatal(err)
	}
	e := m.Effective()
	if e.Filter.CNAMEEnabled() || e.Cache.ServeStaleEnabled() || e.Cache.PrefetchEnabled() || e.Cache.StaleMax != 6*time.Hour {
		t.Errorf("effectif: %+v %+v", e.Filter, e.Cache)
	}
	if e.Cache.StaleWindow() != 0 {
		t.Error("secours désactivé : durée effective nulle")
	}

	m2, _ := Open(path, baseConfig(t)) // redémarrage
	if m2.Effective().Filter.CNAMEEnabled() || m2.Effective().Cache.PrefetchEnabled() {
		t.Error("les nouvelles options doivent survivre au redémarrage")
	}
	if err := m.Update(GroupCache, raw(Cache{Enabled: true, MaxEntries: 500, MaxTTL: "1h", StaleMax: "demain"})); err == nil || !strings.Contains(err.Error(), "stale_max") {
		t.Errorf("stale_max invalide: %v", err)
	}
}

// Un settings.json écrit par une version précédente (sans les nouvelles options) doit rester lisible.
func TestOldSettingsFileStillLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	old := `{"cache":{"enabled":true,"max_entries":777,"max_ttl":"2h"},"filter":{"block_mode":"nxdomain","update_interval":"12h"}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(path, baseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	e := m.Effective()
	if e.Cache.MaxEntries != 777 || e.Filter.BlockMode != "nxdomain" {
		t.Errorf("valeurs de l'ancien fichier perdues: %+v %+v", e.Cache, e.Filter)
	}
	if !e.Filter.CNAMEEnabled() || !e.Cache.ServeStaleEnabled() || !e.Cache.PrefetchEnabled() || e.Cache.StaleMax != 24*time.Hour {
		t.Errorf("les options absentes doivent prendre leur valeur par défaut: %+v %+v", e.Filter, e.Cache)
	}
	if _, err := os.Stat(path + ".bad"); err == nil {
		t.Error("un ancien fichier valide ne doit pas être mis de côté")
	}
}

func TestInfraSettingsOverrideAndAreNotEditableFromTheWeb(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	m, _ := Open(path, baseConfig(t))
	if err := m.SetInfra(InfraSettings{DNSListen: "192.168.1.10:53", WebListen: ":9090", TLSCert: "/c.pem", TLSKey: "/k.pem"}); err != nil {
		t.Fatal(err)
	}
	e := m.Effective()
	if e.DNS.Listen != "192.168.1.10:53" || e.Web.Listen != ":9090" || e.Web.TLSCert != "/c.pem" {
		t.Errorf("effectif: %+v %+v", e.DNS.Listen, e.Web)
	}
	if v := m.View().Infra; v.DNSListen != "192.168.1.10:53" || v.WebListen != ":9090" || !v.HTTPS {
		t.Errorf("la vue doit afficher les valeurs en vigueur: %+v", v)
	}

	// Survit au redémarrage.
	m2, _ := Open(path, baseConfig(t))
	if m2.Effective().DNS.Listen != "192.168.1.10:53" {
		t.Error("les adresses d'écoute doivent survivre au redémarrage")
	}

	// L'API web (Update) ne doit PAS pouvoir y toucher : risque d'auto-verrouillage.
	if err := m.Update("infra", raw(InfraSettings{WebListen: ":1"})); err == nil {
		t.Error("le groupe infra ne doit pas être modifiable via Update")
	}

	// Validation.
	for _, bad := range []InfraSettings{{DNSListen: "pas-une-adresse"}, {WebListen: "192.168.1.10"}, {TLSCert: "/c.pem"}} {
		if err := m.SetInfra(bad); err == nil {
			t.Errorf("%+v devrait être refusé", bad)
		}
	}

	// Retour à config.yaml.
	if err := m.ClearInfra(); err != nil {
		t.Fatal(err)
	}
	if e := m.Effective(); e.DNS.Listen != ":53" || e.Web.TLSCert != "" {
		t.Errorf("après ClearInfra: %+v %+v", e.DNS.Listen, e.Web)
	}
}
