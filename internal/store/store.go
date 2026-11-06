// Package store garde les réglages modifiables depuis l'interface web
// (allowlist, blocklist, listes de filtres ajoutées) et les applique au moteur.
//
// Deux sources de réglages coexistent :
//   - config.yaml : réglages "fixes", écrits à la main. L'interface les affiche
//     mais ne peut pas les supprimer.
//   - state.json  : réglages ajoutés depuis l'interface web, sauvegardés ici.
//
// Le moteur de filtrage reçoit l'union des deux.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/x3Lo/efBlock/internal/filter"
)

// Kind désigne une des deux listes de domaines.
type Kind string

const (
	Allow Kind = "allow" // allowlist : jamais bloqués
	Block Kind = "block" // blocklist : toujours bloqués
)

// Origine d'une entrée.
const (
	SourceConfig = "config" // vient de config.yaml : non modifiable ici
	SourceUI     = "ui"     // ajoutée depuis l'interface
)

// Limites : un garde-fou contre les erreurs et les abus.
const (
	maxDomains         = 5000
	maxLists           = 30
	maxGroups          = 30
	maxGroupClients    = 200
	maxGroupDomains    = 1000
	maxGroupNameLength = 40
)

var (
	ErrInvalid  = errors.New("valeur invalide")
	ErrReadOnly = errors.New("défini dans config.yaml : à modifier dans le fichier")
	ErrNotFound = errors.New("introuvable")
	ErrTooMany  = errors.New("limite atteinte")
)

// DomainEntry est une ligne de l'allowlist ou de la blocklist.
type DomainEntry struct {
	Domain string `json:"domain"`
	Source string `json:"source"`
}

// ListEntry est une liste de filtres configurée.
type ListEntry struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Source string `json:"source"`
}

// Group : des clients qui ont leurs propres règles de filtrage.
type Group struct {
	Name     string   `json:"name"`
	Clients  []string `json:"clients"`   // adresses ("192.168.1.50") ou réseaux ("192.168.1.0/24")
	Mode     string   `json:"mode"`      // "custom" : filtrage avec règles propres ; "off" : aucun blocage
	AllLists bool     `json:"all_lists"` // true : toutes les listes de filtres
	Lists    []string `json:"lists"`     // sinon, seulement ces listes (leurs adresses)
	Allow    []string `json:"allow"`     // domaines jamais bloqués pour ce groupe
	Block    []string `json:"block"`     // domaines toujours bloqués pour ce groupe
}

// Modes d'un groupe.
const (
	ModeCustom = "custom"
	ModeOff    = "off"
)

// state est ce qui est sauvegardé sur disque.
type state struct {
	Allowlist []string    `json:"allowlist"`
	Blocklist []string    `json:"blocklist"`
	Lists     []stateList `json:"lists"`
	Groups    []Group     `json:"groups"`
}

type stateList struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Store est sûr pour un usage concurrent.
type Store struct {
	mu      sync.Mutex
	path    string
	st      state
	cfg     state // réglages de config.yaml (même forme)
	engine  *filter.Engine
	updater *filter.Updater
}

// Open charge state.json (absent = vide) et applique immédiatement l'ensemble au moteur.
func Open(path string, cfgAllow, cfgBlock []string, cfgLists []filter.Source, eng *filter.Engine, upd *filter.Updater) (*Store, error) {
	s := &Store{path: path, engine: eng, updater: upd}
	s.cfg = state{Allowlist: cfgAllow, Blocklist: cfgBlock}
	for _, l := range cfgLists {
		s.cfg.Lists = append(s.cfg.Lists, stateList{l.Name, l.URL})
	}

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &s.st); err != nil {
			return nil, fmt.Errorf("%s illisible: %w", path, err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	s.apply(false)
	return s, nil
}

// apply envoie l'union (config + interface) au moteur et au téléchargeur de listes.
// Doit être appelée avec le verrou, ou avant que le Store soit partagé.
func (s *Store) apply(refreshLists bool) {
	s.engine.SetUserRules(
		append(append([]string(nil), s.cfg.Allowlist...), s.st.Allowlist...),
		append(append([]string(nil), s.cfg.Blocklist...), s.st.Blocklist...),
	)
	if err := s.engine.SetGroups(groupSpecs(s.st.Groups)); err != nil {
		slog.Warn("groupes de clients ignorés", "err", err) // ne peut arriver que si state.json a été modifié à la main
	}
	var sources []filter.Source
	for _, l := range s.allLists() {
		sources = append(sources, filter.Source{Name: l.Name, URL: l.URL})
	}
	s.updater.SetSources(sources)
	if refreshLists {
		s.updater.Trigger()
	}
}

func (s *Store) allLists() []ListEntry {
	var out []ListEntry
	seen := map[string]bool{}
	for _, l := range s.cfg.Lists {
		if !seen[l.URL] {
			seen[l.URL] = true
			out = append(out, ListEntry{l.Name, l.URL, SourceConfig})
		}
	}
	for _, l := range s.st.Lists {
		if !seen[l.URL] {
			seen[l.URL] = true
			out = append(out, ListEntry{l.Name, l.URL, SourceUI})
		}
	}
	return out
}

// save écrit state.json de façon atomique (fichier temporaire puis renommage).
func (s *Store) save() error {
	data, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// kindLists retourne les listes (config, interface) correspondant à kind.
func (s *Store) kindLists(k Kind) (cfg []string, ui *[]string) {
	if k == Allow {
		return s.cfg.Allowlist, &s.st.Allowlist
	}
	return s.cfg.Blocklist, &s.st.Blocklist
}

// Domains retourne les entrées de l'allowlist ou de la blocklist.
func (s *Store) Domains(k Kind) []DomainEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, ui := s.kindLists(k)
	out := []DomainEntry{}
	seen := map[string]bool{}
	for _, d := range cfg {
		if n, ok := filter.ValidateDomain(d); ok && !seen[n] {
			seen[n] = true
			out = append(out, DomainEntry{n, SourceConfig})
		}
	}
	mine := append([]string(nil), *ui...)
	sort.Strings(mine)
	for _, d := range mine {
		if !seen[d] {
			seen[d] = true
			out = append(out, DomainEntry{d, SourceUI})
		}
	}
	return out
}

// AddDomain ajoute un domaine (sous-domaines inclus). Ajouter deux fois est sans effet.
func (s *Store) AddDomain(k Kind, domain string) error {
	d, ok := filter.ValidateDomain(strings.TrimSpace(domain))
	if !ok {
		return fmt.Errorf("%w: %q n'est pas un nom de domaine", ErrInvalid, domain)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, ui := s.kindLists(k)
	for _, x := range append(append([]string(nil), cfg...), *ui...) {
		if n, _ := filter.ValidateDomain(x); n == d {
			return nil
		}
	}
	if len(*ui) >= maxDomains {
		return ErrTooMany
	}
	*ui = append(*ui, d)
	if err := s.save(); err != nil {
		*ui = (*ui)[:len(*ui)-1]
		return err
	}
	s.apply(false)
	return nil
}

// RemoveDomain retire un domaine ajouté depuis l'interface.
func (s *Store) RemoveDomain(k Kind, domain string) error {
	d, ok := filter.ValidateDomain(strings.TrimSpace(domain))
	if !ok {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, ui := s.kindLists(k)
	for i, x := range *ui {
		if x == d {
			old := append([]string(nil), *ui...)
			*ui = append((*ui)[:i], (*ui)[i+1:]...)
			if err := s.save(); err != nil {
				*ui = old
				return err
			}
			s.apply(false)
			return nil
		}
	}
	for _, x := range cfg {
		if n, _ := filter.ValidateDomain(x); n == d {
			return ErrReadOnly
		}
	}
	return ErrNotFound
}

// Lists retourne toutes les listes de filtres (config d'abord).
func (s *Store) Lists() []ListEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.allLists()
}

// AddList ajoute une liste de filtres (URL http ou https) et lance son téléchargement.
func (s *Store) AddList(name, rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: l'adresse doit commencer par http:// ou https://", ErrInvalid)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = u.Host
	}
	if len(name) > 80 || len(rawURL) > 500 {
		return fmt.Errorf("%w: nom ou adresse trop long", ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.allLists() {
		if l.URL == rawURL {
			return nil
		}
	}
	if len(s.allLists()) >= maxLists {
		return ErrTooMany
	}
	s.st.Lists = append(s.st.Lists, stateList{name, rawURL})
	if err := s.save(); err != nil {
		s.st.Lists = s.st.Lists[:len(s.st.Lists)-1]
		return err
	}
	s.apply(true)
	return nil
}

// RemoveList retire une liste ajoutée depuis l'interface.
func (s *Store) RemoveList(rawURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, l := range s.st.Lists {
		if l.URL == rawURL {
			old := append([]stateList(nil), s.st.Lists...)
			oldGroups := cloneGroups(s.st.Groups)
			s.st.Lists = append(s.st.Lists[:i], s.st.Lists[i+1:]...)
			for gi := range s.st.Groups { // les groupes ne doivent pas garder une référence à une liste disparue
				kept := []string{}
				for _, u := range s.st.Groups[gi].Lists {
					if u != rawURL {
						kept = append(kept, u)
					}
				}
				s.st.Groups[gi].Lists = kept
			}
			if err := s.save(); err != nil {
				s.st.Lists = old
				s.st.Groups = oldGroups
				return err
			}
			s.apply(true) // reconstruit l'index sans cette liste
			return nil
		}
	}
	for _, l := range s.cfg.Lists {
		if l.URL == rawURL {
			return ErrReadOnly
		}
	}
	return ErrNotFound
}

// groupSpecs convertit les groupes enregistrés en règles pour le moteur de filtrage.
func groupSpecs(gs []Group) []filter.GroupSpec {
	specs := make([]filter.GroupSpec, 0, len(gs))
	for _, g := range gs {
		specs = append(specs, filter.GroupSpec{
			Name: g.Name, Clients: g.Clients, Off: g.Mode == ModeOff,
			AllLists: g.AllLists, Lists: g.Lists, Allow: g.Allow, Block: g.Block,
		})
	}
	return specs
}

// Groups retourne les groupes de clients, dans l'ordre (le premier groupe qui contient un client s'applique).
func (s *Store) Groups() []Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneGroups(s.st.Groups)
}

func cloneGroups(gs []Group) []Group {
	out := make([]Group, 0, len(gs))
	for _, g := range gs {
		g.Clients = append([]string{}, g.Clients...)
		g.Lists = append([]string{}, g.Lists...)
		g.Allow = append([]string{}, g.Allow...)
		g.Block = append([]string{}, g.Block...)
		out = append(out, g)
	}
	return out
}

// SetGroups remplace tous les groupes. Tout est validé avant d'être appliqué : en cas
// d'erreur, rien n'est modifié.
func (s *Store) SetGroups(in []Group) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	known := map[string]bool{}
	for _, l := range s.allLists() {
		known[l.URL] = true
	}
	out, err := validateGroups(in, known)
	if err != nil {
		return err
	}

	// Le moteur est le dernier juge : s'il refuse, on n'enregistre rien.
	if err := s.engine.SetGroups(groupSpecs(out)); err != nil {
		_ = s.engine.SetGroups(groupSpecs(s.st.Groups)) // retour à l'état précédent
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	old := s.st.Groups
	s.st.Groups = out
	if err := s.save(); err != nil {
		s.st.Groups = old
		_ = s.engine.SetGroups(groupSpecs(old))
		return err
	}
	return nil
}

// validateGroups vérifie et normalise des groupes ; known : adresses des listes existantes.
func validateGroups(in []Group, known map[string]bool) ([]Group, error) {
	if len(in) > maxGroups {
		return nil, fmt.Errorf("%w: %d groupes au maximum", ErrTooMany, maxGroups)
	}
	var out []Group
	seen := map[string]bool{}
	for _, g := range in {
		g.Name = strings.TrimSpace(g.Name)
		if g.Name == "" || len(g.Name) > maxGroupNameLength {
			return nil, fmt.Errorf("%w: le nom d'un groupe doit faire entre 1 et %d caractères", ErrInvalid, maxGroupNameLength)
		}
		if seen[strings.ToLower(g.Name)] {
			return nil, fmt.Errorf("%w: deux groupes s'appellent « %s »", ErrInvalid, g.Name)
		}
		seen[strings.ToLower(g.Name)] = true
		if g.Mode != ModeCustom && g.Mode != ModeOff {
			return nil, fmt.Errorf("%w: mode inconnu pour « %s »", ErrInvalid, g.Name)
		}

		if len(g.Clients) == 0 {
			return nil, fmt.Errorf("%w: le groupe « %s » n'a aucun client", ErrInvalid, g.Name)
		}
		if len(g.Clients) > maxGroupClients {
			return nil, fmt.Errorf("%w: « %s » : %d clients au maximum", ErrTooMany, g.Name, maxGroupClients)
		}
		clients := make([]string, 0, len(g.Clients))
		for _, c := range g.Clients {
			if _, err := filter.ParseClient(c); err != nil {
				return nil, fmt.Errorf("%w: groupe « %s » : %v", ErrInvalid, g.Name, err)
			}
			clients = append(clients, strings.TrimSpace(c))
		}
		g.Clients = clients

		g.Allow, g.Block = nil2empty(g.Allow), nil2empty(g.Block)
		var err error
		if g.Allow, err = normalizeDomains(g.Name, g.Allow); err != nil {
			return nil, err
		}
		if g.Block, err = normalizeDomains(g.Name, g.Block); err != nil {
			return nil, err
		}

		if g.AllLists {
			g.Lists = []string{}
		} else {
			for _, u := range g.Lists {
				if !known[u] {
					return nil, fmt.Errorf("%w: groupe « %s » : liste inconnue (%s)", ErrInvalid, g.Name, u)
				}
			}
			g.Lists = nil2empty(g.Lists)
		}
		out = append(out, g)
	}

	return out, nil
}

// Backup : ce qui a été ajouté depuis l'interface (les éléments de config.yaml restent dans ce fichier).
type Backup struct {
	Allowlist []string     `json:"allowlist"`
	Blocklist []string     `json:"blocklist"`
	Lists     []BackupList `json:"lists"`
	Groups    []Group      `json:"groups"`
}

// BackupList : une liste de filtres ajoutée depuis l'interface.
type BackupList struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Backup retourne une copie de ce qui a été ajouté depuis l'interface.
func (s *Store) Backup() Backup {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := Backup{
		Allowlist: append([]string{}, s.st.Allowlist...),
		Blocklist: append([]string{}, s.st.Blocklist...),
		Lists:     []BackupList{},
		Groups:    append([]Group{}, s.st.Groups...),
	}
	for _, l := range s.st.Lists {
		b.Lists = append(b.Lists, BackupList{Name: l.Name, URL: l.URL})
	}
	return b
}

// Restore remplace tout ce qui a été ajouté depuis l'interface par le contenu d'une sauvegarde.
// Tout est vérifié avant d'appliquer quoi que ce soit : en cas d'erreur, rien n'est changé.
func (s *Store) Restore(b Backup) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var next state
	var err error
	if next.Allowlist, err = restoreDomains("allowlist", b.Allowlist); err != nil {
		return err
	}
	if next.Blocklist, err = restoreDomains("blocklist", b.Blocklist); err != nil {
		return err
	}

	known := map[string]bool{}
	for _, l := range s.cfg.Lists {
		known[l.URL] = true
	}
	next.Lists = []stateList{}
	for _, l := range b.Lists {
		raw := strings.TrimSpace(l.URL)
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%w: liste « %s » : adresse invalide", ErrInvalid, l.Name)
		}
		name := strings.TrimSpace(l.Name)
		if name == "" {
			name = u.Host
		}
		if len(name) > 80 || len(raw) > 500 {
			return fmt.Errorf("%w: liste « %s » : nom ou adresse trop long", ErrInvalid, name)
		}
		if known[raw] {
			continue // déjà là (config.yaml, ou doublon dans la sauvegarde)
		}
		known[raw] = true
		next.Lists = append(next.Lists, stateList{name, raw})
	}
	if len(s.cfg.Lists)+len(next.Lists) > maxLists {
		return fmt.Errorf("%w: %d listes au maximum", ErrTooMany, maxLists)
	}

	if next.Groups, err = validateGroups(b.Groups, known); err != nil {
		return err
	}
	next.Groups = append([]Group{}, next.Groups...)

	// Le moteur est le dernier juge : s'il refuse les groupes, on n'enregistre rien.
	if err := s.engine.SetGroups(groupSpecs(next.Groups)); err != nil {
		_ = s.engine.SetGroups(groupSpecs(s.st.Groups))
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	old := s.st
	s.st = next
	if err := s.save(); err != nil {
		s.st = old
		s.apply(false)
		return err
	}
	s.apply(true) // nouvelles listes : téléchargement tout de suite
	return nil
}

// restoreDomains valide les domaines d'une sauvegarde (sans doublon).
func restoreDomains(what string, in []string) ([]string, error) {
	if len(in) > maxDomains {
		return nil, fmt.Errorf("%w: %s : %d domaines au maximum", ErrTooMany, what, maxDomains)
	}
	out := []string{}
	seen := map[string]bool{}
	for _, d := range in {
		n, ok := filter.ValidateDomain(strings.TrimSpace(d))
		if !ok {
			return nil, fmt.Errorf("%w: %s : %q n'est pas un nom de domaine", ErrInvalid, what, d)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

func nil2empty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// normalizeDomains valide et normalise des domaines (sans doublon).
func normalizeDomains(group string, in []string) ([]string, error) {
	if len(in) > maxGroupDomains {
		return nil, fmt.Errorf("%w: groupe « %s » : %d domaines au maximum par liste", ErrTooMany, group, maxGroupDomains)
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, d := range in {
		n, ok := filter.ValidateDomain(strings.TrimSpace(d))
		if !ok {
			return nil, fmt.Errorf("%w: groupe « %s » : %q n'est pas un nom de domaine", ErrInvalid, group, d)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}
