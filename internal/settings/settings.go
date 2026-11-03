// Package settings permet de modifier la configuration depuis l'interface web.
//
// Principe : config.yaml reste la base. Quand tu enregistres un groupe de réglages
// depuis l'interface, ses valeurs sont écrites dans settings.json (dossier de
// données, que le service a le droit d'écrire, contrairement à /etc) et
// REMPLACENT celles du fichier pour ce groupe. "Rétablir" supprime ce remplacement.
//
// Les adresses d'écoute et le TLS restent volontairement dans config.yaml : une
// faute de frappe dans l'interface te couperait l'accès à l'interface elle-même.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/x3Lo/efBlock/internal/config"
)

// Noms des groupes de réglages.
const (
	GroupUpstream = "upstream"
	GroupAccess   = "access"
	GroupLocal    = "local"
	GroupFilter   = "filter"
	GroupCache    = "cache"
	GroupQueryLog = "querylog"
)

// ErrInvalid : une valeur est refusée.
var ErrInvalid = errors.New("réglage invalide")

// Les groupes tels que l'interface les échange (JSON).

// Upstream : vers qui sont envoyées les requêtes.
type Upstream struct {
	Upstreams []string `json:"upstreams"`
	Bootstrap []string `json:"bootstrap"`
	Timeout   string   `json:"timeout"`
}

// Access : qui a le droit d'utiliser le DNS et l'interface.
type Access struct {
	DNSClients []string `json:"dns_clients"`
	WebCIDRs   []string `json:"web_cidrs"`
}

// Local : noms du réseau local.
type Local struct {
	Upstream string   `json:"upstream"`
	Domains  []string `json:"domains"`
	Hosts    []string `json:"hosts"`
}

// Filter : comportement du blocage.
type Filter struct {
	BlockMode      string `json:"block_mode"`
	UpdateInterval string `json:"update_interval"`
}

// Cache : cache DNS.
type Cache struct {
	Enabled    bool   `json:"enabled"`
	MaxEntries int    `json:"max_entries"`
	MaxTTL     string `json:"max_ttl"`
}

// QueryLog : journal des requêtes.
type QueryLog struct {
	RetentionDays int `json:"retention_days"`
}

// Overrides : ce qui a été modifié depuis l'interface (nil = non modifié).
type Overrides struct {
	Upstream     *Upstream `json:"upstream,omitempty"`
	Access       *Access   `json:"access,omitempty"`
	Local        *Local    `json:"local,omitempty"`
	Filter       *Filter   `json:"filter,omitempty"`
	Cache        *Cache    `json:"cache,omitempty"`
	QueryLog     *QueryLog `json:"querylog,omitempty"`
	PasswordHash string    `json:"password_hash,omitempty"`
}

// Groups : toutes les valeurs actuelles, groupe par groupe.
type Groups struct {
	Upstream Upstream `json:"upstream"`
	Access   Access   `json:"access"`
	Local    Local    `json:"local"`
	Filter   Filter   `json:"filter"`
	Cache    Cache    `json:"cache"`
	QueryLog QueryLog `json:"querylog"`
}

// Infra : réglages qui ne se changent que dans config.yaml (lecture seule dans l'interface).
type Infra struct {
	DNSListen       string `json:"dns_listen"`
	WebListen       string `json:"web_listen"`
	HTTPS           bool   `json:"https"`
	DataDir         string `json:"data_dir"`
	QueryLogEnabled bool   `json:"querylog_enabled"`
}

// View : ce que l'interface affiche.
type View struct {
	Effective  Groups   `json:"effective"`  // valeurs en vigueur
	Base       Groups   `json:"base"`       // valeurs de config.yaml
	Overridden []string `json:"overridden"` // groupes modifiés depuis l'interface
	Infra      Infra    `json:"infra"`
}

// Manager garde la configuration effective et applique les changements.
type Manager struct {
	mu    sync.Mutex
	path  string
	base  *config.Config
	ov    Overrides
	eff   *config.Config
	apply func(*config.Config) error // met la configuration en application (à chaud)
}

// Open charge settings.json (absent = aucun réglage modifié).
// Si le fichier est illisible ou invalide, il est mis de côté (.bad) et ignoré :
// un fichier abîmé ne doit jamais empêcher efBlock de démarrer.
func Open(path string, base *config.Config) (*Manager, error) {
	m := &Manager{path: path, base: base}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		var ov Overrides
		dec := json.NewDecoder(bytes.NewReader(data))
		if derr := dec.Decode(&ov); derr == nil {
			if eff, berr := m.build(ov); berr == nil {
				m.ov, m.eff = ov, eff
				return m, nil
			} else {
				derr = berr
				slog.Warn("settings.json invalide : réglages de l'interface ignorés", "err", derr)
			}
		} else {
			slog.Warn("settings.json illisible : réglages de l'interface ignorés", "err", derr)
		}
		_ = os.Rename(path, path+".bad")
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	eff, err := m.build(Overrides{})
	if err != nil {
		return nil, err
	}
	m.eff = eff
	return m, nil
}

// SetApply définit la fonction qui met une configuration en application.
func (m *Manager) SetApply(f func(*config.Config) error) {
	m.mu.Lock()
	m.apply = f
	m.mu.Unlock()
}

// Effective retourne la configuration en vigueur (copie : on peut la lire sans verrou).
func (m *Manager) Effective() *config.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *m.eff
	return &c
}

// build calcule la configuration effective = base + remplacements.
func (m *Manager) build(ov Overrides) (*config.Config, error) {
	c := *m.base // copie : la base n'est jamais modifiée
	if g := ov.Upstream; g != nil {
		c.DNS.Upstreams = append([]string(nil), g.Upstreams...)
		c.DNS.Bootstrap = append([]string(nil), g.Bootstrap...)
		d, err := parseDuration("timeout", g.Timeout)
		if err != nil {
			return nil, err
		}
		c.DNS.Timeout = d
	}
	if g := ov.Access; g != nil {
		c.DNS.AllowedClients = append([]string(nil), g.DNSClients...)
		c.Web.AllowedCIDRs = append([]string(nil), g.WebCIDRs...)
	}
	if g := ov.Local; g != nil {
		c.DNS.Local = config.Local{Upstream: g.Upstream, Domains: append([]string(nil), g.Domains...), Hosts: append([]string(nil), g.Hosts...)}
	}
	if g := ov.Filter; g != nil {
		c.Filter.BlockMode = g.BlockMode
		d, err := parseDuration("update_interval", g.UpdateInterval)
		if err != nil {
			return nil, err
		}
		c.Filter.UpdateInterval = d
	}
	if g := ov.Cache; g != nil {
		enabled := g.Enabled
		c.Cache.Enabled = &enabled
		c.Cache.MaxEntries = g.MaxEntries
		d, err := parseDuration("max_ttl", g.MaxTTL)
		if err != nil {
			return nil, err
		}
		c.Cache.MaxTTL = d
	}
	if g := ov.QueryLog; g != nil {
		c.Log.RetentionDays = g.RetentionDays
	}
	if ov.PasswordHash != "" {
		c.Web.PasswordHash = ov.PasswordHash
	}
	// Les listes calculées par Finalize doivent être recalculées à partir des nouvelles valeurs.
	c.DNS.AllowedNets, c.Web.AllowedNets = nil, nil
	if err := c.Finalize(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return &c, nil
}

func parseDuration(field, s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%w: %s doit être une durée comme 3s, 30m ou 24h (reçu %q)", ErrInvalid, field, s)
	}
	return d, nil
}

// Update remplace un groupe de réglages : validation, mise en application à chaud,
// puis sauvegarde. Si l'une des étapes échoue, rien n'est changé.
func (m *Manager) Update(group string, raw json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ov := m.ov // copie de la structure (les pointeurs sont remplacés, jamais modifiés)
	dec := func(v any) error {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(v); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		return nil
	}
	switch group {
	case GroupUpstream:
		g := &Upstream{}
		if err := dec(g); err != nil {
			return err
		}
		ov.Upstream = g
	case GroupAccess:
		g := &Access{}
		if err := dec(g); err != nil {
			return err
		}
		ov.Access = g
	case GroupLocal:
		g := &Local{}
		if err := dec(g); err != nil {
			return err
		}
		ov.Local = g
	case GroupFilter:
		g := &Filter{}
		if err := dec(g); err != nil {
			return err
		}
		ov.Filter = g
	case GroupCache:
		g := &Cache{}
		if err := dec(g); err != nil {
			return err
		}
		ov.Cache = g
	case GroupQueryLog:
		g := &QueryLog{}
		if err := dec(g); err != nil {
			return err
		}
		ov.QueryLog = g
	default:
		return fmt.Errorf("%w: groupe inconnu %q", ErrInvalid, group)
	}
	return m.commit(ov)
}

// Reset supprime le remplacement d'un groupe : retour aux valeurs de config.yaml.
func (m *Manager) Reset(group string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ov := m.ov
	switch group {
	case GroupUpstream:
		ov.Upstream = nil
	case GroupAccess:
		ov.Access = nil
	case GroupLocal:
		ov.Local = nil
	case GroupFilter:
		ov.Filter = nil
	case GroupCache:
		ov.Cache = nil
	case GroupQueryLog:
		ov.QueryLog = nil
	default:
		return fmt.Errorf("%w: groupe inconnu %q", ErrInvalid, group)
	}
	return m.commit(ov)
}

// SetPasswordHash enregistre un nouveau mot de passe (hash bcrypt) et l'applique.
func (m *Manager) SetPasswordHash(hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ov := m.ov
	ov.PasswordHash = hash
	return m.commit(ov)
}

// commit : construit, applique, sauvegarde. Doit être appelée avec le verrou.
func (m *Manager) commit(ov Overrides) error {
	eff, err := m.build(ov)
	if err != nil {
		return err
	}
	if m.apply != nil {
		if err := m.apply(eff); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	m.ov, m.eff = ov, eff
	return m.save()
}

// save écrit settings.json de façon atomique (fichier temporaire puis renommage), en 0600.
func (m *Manager) save() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.ov, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

// View décrit l'état actuel pour l'interface.
func (m *Manager) View() View {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := View{Effective: groupsOf(m.eff), Base: groupsOf(m.base), Overridden: []string{}}
	add := func(name string, set bool) {
		if set {
			v.Overridden = append(v.Overridden, name)
		}
	}
	add(GroupUpstream, m.ov.Upstream != nil)
	add(GroupAccess, m.ov.Access != nil)
	add(GroupLocal, m.ov.Local != nil)
	add(GroupFilter, m.ov.Filter != nil)
	add(GroupCache, m.ov.Cache != nil)
	add(GroupQueryLog, m.ov.QueryLog != nil)
	v.Infra = Infra{
		DNSListen: m.base.DNS.Listen, WebListen: m.base.Web.Listen, HTTPS: m.base.Web.TLSCert != "",
		DataDir: m.base.Filter.DataDir, QueryLogEnabled: m.base.Log.IsEnabled(),
	}
	return v
}

func groupsOf(c *config.Config) Groups {
	nz := func(s []string) []string {
		if s == nil {
			return []string{} // JSON : [] plutôt que null
		}
		return s
	}
	return Groups{
		Upstream: Upstream{Upstreams: nz(c.DNS.Upstreams), Bootstrap: nz(c.DNS.Bootstrap), Timeout: fmtDuration(c.DNS.Timeout)},
		Access:   Access{DNSClients: nz(c.DNS.AllowedClients), WebCIDRs: nz(c.Web.AllowedCIDRs)},
		Local:    Local{Upstream: c.DNS.Local.Upstream, Domains: nz(c.DNS.Local.Domains), Hosts: nz(c.DNS.Local.Hosts)},
		Filter:   Filter{BlockMode: c.Filter.BlockMode, UpdateInterval: fmtDuration(c.Filter.UpdateInterval)},
		Cache:    Cache{Enabled: c.Cache.IsEnabled(), MaxEntries: c.Cache.MaxEntries, MaxTTL: fmtDuration(c.Cache.MaxTTL)},
		QueryLog: QueryLog{RetentionDays: c.Log.RetentionDays},
	}
}

// fmtDuration écrit une durée de façon lisible : 24h plutôt que 24h0m0s.
func fmtDuration(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d%time.Second == 0:
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return d.String()
}
