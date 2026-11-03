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
	maxDomains = 5000
	maxLists   = 30
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

// state est ce qui est sauvegardé sur disque.
type state struct {
	Allowlist []string    `json:"allowlist"`
	Blocklist []string    `json:"blocklist"`
	Lists     []stateList `json:"lists"`
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
			s.st.Lists = append(s.st.Lists[:i], s.st.Lists[i+1:]...)
			if err := s.save(); err != nil {
				s.st.Lists = old
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
