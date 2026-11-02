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
	DNS DNS `yaml:"dns"`
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

	if len(cfg.DNS.Upstreams) == 0 {
		return nil, fmt.Errorf("dns.upstreams: au moins un résolveur amont est requis")
	}
	return cfg, nil
}
