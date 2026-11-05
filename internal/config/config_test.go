package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMinimalAppliesDefaults(t *testing.T) {
	cfg, err := Load(write(t, "dns:\n  upstreams: [\"1.1.1.1\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DNS.Listen != ":53" || cfg.DNS.Timeout != 3*time.Second {
		t.Errorf("dns: %+v", cfg.DNS)
	}
	if cfg.Filter.BlockMode != "zero_ip" || cfg.Filter.UpdateInterval != 24*time.Hour || cfg.Filter.DataDir != "./data" {
		t.Errorf("filter: %+v", cfg.Filter)
	}
	if !cfg.Cache.IsEnabled() || cfg.Cache.MaxEntries != 10000 {
		t.Errorf("cache: %+v", cfg.Cache)
	}
	if !cfg.Log.IsEnabled() || cfg.Log.RetentionDays != 7 || cfg.Log.Path != filepath.Join("./data", "queries.db") {
		t.Errorf("log: %+v", cfg.Log)
	}
	if len(cfg.Web.AllowedNets) == 0 || len(cfg.DNS.AllowedNets) == 0 {
		t.Error("les réseaux autorisés doivent avoir une valeur par défaut (réseaux privés)")
	}
}

func TestDefaultAccessIsPrivateNetworksOnly(t *testing.T) {
	cfg, err := Load(write(t, "dns:\n  upstreams: [\"1.1.1.1\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	contains := func(ip string) bool {
		for _, n := range cfg.DNS.AllowedNets {
			if n.Contains(netIP(ip)) {
				return true
			}
		}
		return false
	}
	for _, ip := range []string{"192.168.1.5", "10.0.0.2", "172.20.1.1", "127.0.0.1", "::1", "fd00::1"} {
		if !contains(ip) {
			t.Errorf("%s devrait être autorisée par défaut", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "172.32.0.1", "2001:db8::1"} {
		if contains(ip) {
			t.Errorf("%s ne devrait PAS être autorisée par défaut", ip)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct{ name, yaml, want string }{
		{"pas d'upstream", "dns:\n  listen: \":53\"\n", "upstreams"},
		{"block_mode inconnu", "dns:\n  upstreams: [\"1.1.1.1\"]\nfilter:\n  block_mode: boum\n", "block_mode"},
		{"intervalle trop court", "dns:\n  upstreams: [\"1.1.1.1\"]\nfilter:\n  update_interval: 5s\n", "update_interval"},
		{"liste sans url", "dns:\n  upstreams: [\"1.1.1.1\"]\nfilter:\n  lists:\n    - name: x\n", "url manquante"},
		{"cidr invalide (web)", "dns:\n  upstreams: [\"1.1.1.1\"]\nweb:\n  allowed_cidrs: [\"pas-un-reseau\"]\n", "web.allowed_cidrs"},
		{"cidr invalide (dns)", "dns:\n  upstreams: [\"1.1.1.1\"]\n  allowed_clients: [\"192.168.1.0/99\"]\n", "dns.allowed_clients"},
		{"tls incomplet", "dns:\n  upstreams: [\"1.1.1.1\"]\nweb:\n  tls_cert: a.pem\n", "tls_cert"},
		{"cache négatif", "dns:\n  upstreams: [\"1.1.1.1\"]\ncache:\n  max_entries: -5\n", "max_entries"},
		{"domaines locaux sans upstream", "dns:\n  upstreams: [\"1.1.1.1\"]\n  local:\n    domains: [lan]\n", "dns.local.upstream"},
		{"YAML cassé", "dns: [\n", "YAML"},
	}
	for _, tt := range tests {
		_, err := Load(write(t, tt.yaml))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s : erreur = %v, attendu un message contenant %q", tt.name, err, tt.want)
		}
	}
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("un fichier absent doit échouer")
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	cfg, err := Load("../../deploy/config.example.yaml")
	if err != nil {
		t.Fatalf("la config d'exemple livrée doit toujours être valide: %v", err)
	}
	if len(cfg.DNS.Upstreams) == 0 || len(cfg.Filter.Lists) == 0 {
		t.Errorf("exemple incomplet: %+v", cfg)
	}
}

func TestNewOptionDefaultsAndOverrides(t *testing.T) {
	cfg, err := Load(write(t, "dns:\n  upstreams: [\"1.1.1.1\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Filter.CNAMEEnabled() || !cfg.Cache.ServeStaleEnabled() || !cfg.Cache.PrefetchEnabled() {
		t.Error("alias, secours et préchargement sont activés par défaut")
	}
	if cfg.Cache.StaleMax != 24*time.Hour || cfg.Cache.StaleWindow() != 24*time.Hour {
		t.Errorf("secours par défaut: %v", cfg.Cache.StaleMax)
	}

	cfg, err = Load(write(t, "dns:\n  upstreams: [\"1.1.1.1\"]\nfilter:\n  cname_check: false\ncache:\n  serve_stale: false\n  prefetch: false\n  stale_max: 2h\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Filter.CNAMEEnabled() || cfg.Cache.ServeStaleEnabled() || cfg.Cache.PrefetchEnabled() || cfg.Cache.StaleMax != 2*time.Hour {
		t.Errorf("valeurs explicites ignorées: %+v %+v", cfg.Filter, cfg.Cache)
	}
	if cfg.Cache.StaleWindow() != 0 {
		t.Error("serve_stale: false : durée effective nulle")
	}
	if _, err := Load(write(t, "dns:\n  upstreams: [\"1.1.1.1\"]\ncache:\n  stale_max: -1h\n")); err == nil {
		t.Error("stale_max négatif doit être refusé")
	}
}
