// Package config charge et valide le fichier de configuration YAML.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config est la racine du fichier de configuration.
type Config struct {
	DNS    DNS    `yaml:"dns"`
	Filter Filter `yaml:"filter"`
	Cache  Cache  `yaml:"cache"`
	Log    Log    `yaml:"querylog"`
	Web    Web    `yaml:"web"`
}

// Web regroupe les réglages de l'interface d'administration.
type Web struct {
	Listen       string   `yaml:"listen"`        // adresse d'écoute, ex "192.168.50.10:8080"
	PasswordHash string   `yaml:"password_hash"` // hash bcrypt (voir: efblock -hash-password). Vide = interface désactivée
	AllowedCIDRs []string `yaml:"allowed_cidrs"` // réseaux autorisés à l'atteindre
	TLSCert      string   `yaml:"tls_cert"`      // optionnel : active HTTPS
	TLSKey       string   `yaml:"tls_key"`

	AllowedNets []*net.IPNet `yaml:"-"` // CIDR analysés (rempli par Load)
}

// defaultAllowedCIDRs : réseaux privés (LAN) et boucle locale. Jamais internet.
var defaultAllowedCIDRs = []string{
	"127.0.0.0/8", "::1/128", // cette machine
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", // IPv4 privées
	"fc00::/7", "fe80::/10", // IPv6 privées
}

// Log regroupe les réglages du journal des requêtes.
type Log struct {
	Enabled       *bool  `yaml:"enabled"`        // nil = activé par défaut
	Path          string `yaml:"path"`           // fichier SQLite
	RetentionDays int    `yaml:"retention_days"` // 0 = valeur par défaut (7), -1 = illimité
}

// IsEnabled indique si le journal est actif (oui, sauf "enabled: false").
func (l Log) IsEnabled() bool { return l.Enabled == nil || *l.Enabled }

// Cache regroupe les réglages du cache DNS.
type Cache struct {
	Enabled    *bool         `yaml:"enabled"`     // nil = activé par défaut
	MaxEntries int           `yaml:"max_entries"` // nombre max de réponses gardées
	MaxTTL     time.Duration `yaml:"max_ttl"`     // plafond de durée de vie d'une réponse

	// ServeStale : servir une ancienne réponse quand aucun upstream ne répond (activé par défaut).
	ServeStale *bool `yaml:"serve_stale"`
	// StaleMax : jusqu'à quel âge une réponse périmée peut servir de secours (24h par défaut).
	StaleMax time.Duration `yaml:"stale_max"`
	// Prefetch : rafraîchir en avance les réponses populaires (activé par défaut).
	Prefetch *bool `yaml:"prefetch"`
}

// ServeStaleEnabled indique si les réponses périmées servent de secours.
func (c Cache) ServeStaleEnabled() bool { return c.ServeStale == nil || *c.ServeStale }

// PrefetchEnabled indique si le préchargement est actif.
func (c Cache) PrefetchEnabled() bool { return c.Prefetch == nil || *c.Prefetch }

// StaleWindow retourne la durée de secours effective (0 si désactivé).
func (c Cache) StaleWindow() time.Duration {
	if !c.ServeStaleEnabled() {
		return 0
	}
	return c.StaleMax
}

// IsEnabled indique si le cache est actif (oui, sauf "enabled: false").
func (c Cache) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// Filter regroupe les réglages du blocage.
type Filter struct {
	DataDir        string        `yaml:"data_dir"`        // cache des listes téléchargées
	BlockMode      string        `yaml:"block_mode"`      // "zero_ip" ou "nxdomain"
	UpdateInterval time.Duration `yaml:"update_interval"` // fréquence de mise à jour des listes
	Lists          []List        `yaml:"lists"`
	Allowlist      []string      `yaml:"allowlist"` // domaines jamais bloqués
	Blocklist      []string      `yaml:"blocklist"` // domaines toujours bloqués

	// CNAMECheck : vérifier aussi les alias (CNAME) des réponses ("CNAME cloaking"). Activé par défaut.
	CNAMECheck *bool `yaml:"cname_check"`
}

// CNAMEEnabled indique si les alias sont vérifiés (oui, sauf "cname_check: false").
func (f Filter) CNAMEEnabled() bool { return f.CNAMECheck == nil || *f.CNAMECheck }

// List est une liste de filtres : URL http(s) ou chemin de fichier local.
type List struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// DNS regroupe les réglages du serveur DNS.
type DNS struct {
	Listen    string        `yaml:"listen"`
	Upstreams []string      `yaml:"upstreams"`
	Bootstrap []string      `yaml:"bootstrap"` // DNS classiques, pour résoudre le nom d'un upstream
	Timeout   time.Duration `yaml:"timeout"`

	// AllowedClients : réseaux autorisés à poser des questions. Sans ça, n'importe
	// qui pouvant joindre le port 53 pourrait se servir de ton serveur ("résolveur ouvert").
	AllowedClients []string `yaml:"allowed_clients"`
	Local          Local    `yaml:"local"`

	AllowedNets []*net.IPNet `yaml:"-"` // AllowedClients analysés (rempli par Load)
}

// Local regroupe les réglages des noms du réseau local (maison, NAS, imprimante...).
type Local struct {
	// Upstream : le résolveur qui connaît les noms du LAN (souvent ta box), ex "192.168.1.1".
	Upstream string `yaml:"upstream"`
	// Domains : suffixes envoyés à Upstream et jamais à internet, ex ["lan", "maison"].
	Domains []string `yaml:"domains"`
	// Hosts : noms fixes, au format du fichier /etc/hosts : "192.168.1.20 nas.maison nas".
	Hosts []string `yaml:"hosts"`
}

// Load lit le fichier YAML, applique les valeurs par défaut puis valide.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("lecture de la config: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("YAML invalide: %w", err)
	}

	if err := cfg.Finalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Finalize applique les valeurs par défaut et valide la configuration. Elle est
// aussi utilisée sur la configuration "effective" (config.yaml + réglages faits
// depuis l'interface web) : on peut donc l'appeler plusieurs fois sans risque.
func (c *Config) Finalize() error {
	var err error
	// Valeurs par défaut : une config minimale doit suffire pour démarrer.
	if c.DNS.Listen == "" {
		c.DNS.Listen = ":53"
	}
	if c.DNS.Timeout == 0 {
		c.DNS.Timeout = 3 * time.Second
	}

	if c.Filter.DataDir == "" {
		c.Filter.DataDir = "./data"
	}
	if c.Filter.BlockMode == "" {
		c.Filter.BlockMode = "zero_ip"
	}
	if c.Filter.BlockMode != "zero_ip" && c.Filter.BlockMode != "nxdomain" {
		return fmt.Errorf("filter.block_mode: %q invalide (zero_ip ou nxdomain)", c.Filter.BlockMode)
	}
	if c.Filter.UpdateInterval == 0 {
		c.Filter.UpdateInterval = 24 * time.Hour
	}
	if c.Filter.UpdateInterval < time.Minute {
		return fmt.Errorf("filter.update_interval: minimum 1m")
	}
	for i, l := range c.Filter.Lists {
		if l.URL == "" {
			return fmt.Errorf("filter.lists[%d]: url manquante", i)
		}
		if l.Name == "" {
			c.Filter.Lists[i].Name = l.URL
		}
	}

	if c.Cache.MaxEntries == 0 {
		c.Cache.MaxEntries = 10000
	}
	if c.Cache.MaxEntries < 0 {
		return fmt.Errorf("cache.max_entries: doit être positif")
	}
	if c.Cache.MaxTTL == 0 {
		c.Cache.MaxTTL = 24 * time.Hour
	}
	if c.Cache.StaleMax == 0 {
		c.Cache.StaleMax = 24 * time.Hour
	}
	if c.Cache.StaleMax < 0 {
		return fmt.Errorf("cache.stale_max: doit être positif")
	}

	if c.Log.Path == "" {
		c.Log.Path = filepath.Join(c.Filter.DataDir, "queries.db")
	}
	if c.Log.RetentionDays == 0 {
		c.Log.RetentionDays = 7
	}

	if c.Web.Listen == "" {
		c.Web.Listen = ":8080"
	}
	if len(c.Web.AllowedCIDRs) == 0 {
		c.Web.AllowedCIDRs = defaultAllowedCIDRs
	}
	if c.Web.AllowedNets, err = parseCIDRs("web.allowed_cidrs", c.Web.AllowedCIDRs); err != nil {
		return err
	}

	// Clients DNS autorisés : par défaut, seulement les réseaux privés.
	if len(c.DNS.AllowedClients) == 0 {
		c.DNS.AllowedClients = defaultAllowedCIDRs
	}
	if c.DNS.AllowedNets, err = parseCIDRs("dns.allowed_clients", c.DNS.AllowedClients); err != nil {
		return err
	}

	// Noms locaux.
	for i, d := range c.DNS.Local.Domains {
		d = strings.Trim(strings.ToLower(strings.TrimSpace(d)), ".")
		if d == "" {
			return fmt.Errorf("dns.local.domains[%d]: domaine vide", i)
		}
		c.DNS.Local.Domains[i] = d
	}
	if len(c.DNS.Local.Domains) > 0 && c.DNS.Local.Upstream == "" {
		return fmt.Errorf("dns.local.domains nécessite dns.local.upstream (le résolveur qui connaît ces noms)")
	}
	if (c.Web.TLSCert == "") != (c.Web.TLSKey == "") {
		return fmt.Errorf("web.tls_cert et web.tls_key vont ensemble")
	}

	if len(c.DNS.Bootstrap) == 0 {
		c.DNS.Bootstrap = []string{"1.1.1.1:53", "9.9.9.9:53"}
	}

	if len(c.DNS.Upstreams) == 0 {
		return fmt.Errorf("dns.upstreams: au moins un résolveur amont est requis")
	}
	return nil
}

// parseCIDRs analyse une liste de réseaux ("192.168.1.0/24", ou "192.168.1.5/32" pour une seule machine).
func parseCIDRs(field string, list []string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, c := range list {
		_, n, err := net.ParseCIDR(strings.TrimSpace(c))
		if err != nil {
			return nil, fmt.Errorf("%s: %q invalide (attendu ex: 192.168.1.0/24)", field, c)
		}
		out = append(out, n)
	}
	return out, nil
}
