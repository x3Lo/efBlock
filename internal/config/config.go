// Package config charge et valide le fichier de configuration YAML.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config est la racine du fichier de configuration.
type Config struct {
	DNS    DNS    `yaml:"dns"`
	Filter Filter `yaml:"filter"`
	Cache  Cache  `yaml:"cache"`
}

// Cache regroupe les réglages du cache DNS.
type Cache struct {
	Enabled    *bool         `yaml:"enabled"`     // nil = activé par défaut
	MaxEntries int           `yaml:"max_entries"` // nombre max de réponses gardées
	MaxTTL     time.Duration `yaml:"max_ttl"`     // plafond de durée de vie d'une réponse
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
}

// List est une liste de filtres : URL http(s) ou chemin de fichier local.
type List struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// DNS regroupe les réglages du serveur DNS.
type DNS struct {
	Listen    string        `yaml:"listen"`
	Upstreams []string      `yaml:"upstreams"`
	Timeout   time.Duration `yaml:"timeout"`
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

	// Valeurs par défaut : une config minimale doit suffire pour démarrer.
	if cfg.DNS.Listen == "" {
		cfg.DNS.Listen = ":53"
	}
	if cfg.DNS.Timeout == 0 {
		cfg.DNS.Timeout = 3 * time.Second
	}

	if cfg.Filter.DataDir == "" {
		cfg.Filter.DataDir = "./data"
	}
	if cfg.Filter.BlockMode == "" {
		cfg.Filter.BlockMode = "zero_ip"
	}
	if cfg.Filter.BlockMode != "zero_ip" && cfg.Filter.BlockMode != "nxdomain" {
		return nil, fmt.Errorf("filter.block_mode: %q invalide (zero_ip ou nxdomain)", cfg.Filter.BlockMode)
	}
	if cfg.Filter.UpdateInterval == 0 {
		cfg.Filter.UpdateInterval = 24 * time.Hour
	}
	if cfg.Filter.UpdateInterval < time.Minute {
		return nil, fmt.Errorf("filter.update_interval: minimum 1m")
	}
	for i, l := range cfg.Filter.Lists {
		if l.URL == "" {
			return nil, fmt.Errorf("filter.lists[%d]: url manquante", i)
		}
		if l.Name == "" {
			cfg.Filter.Lists[i].Name = l.URL
		}
	}

	if cfg.Cache.MaxEntries == 0 {
		cfg.Cache.MaxEntries = 10000
	}
	if cfg.Cache.MaxEntries < 0 {
		return nil, fmt.Errorf("cache.max_entries: doit être positif")
	}
	if cfg.Cache.MaxTTL == 0 {
		cfg.Cache.MaxTTL = 24 * time.Hour
	}

	if len(cfg.DNS.Upstreams) == 0 {
		return nil, fmt.Errorf("dns.upstreams: au moins un résolveur amont est requis")
	}
	return cfg, nil
}
