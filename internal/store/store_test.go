package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/x3Lo/efBlock/internal/filter"
)

func newStore(t *testing.T, cfgAllow, cfgBlock []string) (*Store, *filter.Engine, string) {
	t.Helper()
	dir := t.TempDir()
	eng := filter.NewEngine()
	upd := filter.NewUpdater(eng, nil, dir, time.Hour)
	path := filepath.Join(dir, "state.json")
	s, err := Open(path, cfgAllow, cfgBlock, nil, eng, upd)
	if err != nil {
		t.Fatal(err)
	}
	return s, eng, path
}

func TestAddRemoveAppliesImmediately(t *testing.T) {
	s, eng, _ := newStore(t, nil, nil)

	if err := s.AddDomain(Block, "  Pub.Example.COM. "); err != nil {
		t.Fatal(err)
	}
	if v := eng.Check("sub.pub.example.com"); !v.Blocked {
		t.Error("le domaine ajouté doit être bloqué immédiatement")
	}
	if err := s.AddDomain(Allow, "pub.example.com"); err != nil {
		t.Fatal(err)
	}
	if v := eng.Check("pub.example.com"); v.Blocked {
		t.Error("l'allowlist doit l'emporter")
	}
	if err := s.RemoveDomain(Allow, "pub.example.com"); err != nil {
		t.Fatal(err)
	}
	if v := eng.Check("pub.example.com"); !v.Blocked {
		t.Error("après retrait de l'allowlist, le domaine doit être bloqué à nouveau")
	}
}

func TestValidationAndErrors(t *testing.T) {
	s, _, _ := newStore(t, []string{"fixe.example.com"}, nil)

	for _, bad := range []string{"", "pas un domaine", "com", "http://x.com/a", "192.168.0.1"} {
		if err := s.AddDomain(Block, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("AddDomain(%q) = %v, attendu ErrInvalid", bad, err)
		}
	}
	if err := s.RemoveDomain(Allow, "fixe.example.com"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("retirer une entrée de config.yaml = %v, attendu ErrReadOnly", err)
	}
	if err := s.RemoveDomain(Allow, "inconnu.example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("retirer un inconnu = %v, attendu ErrNotFound", err)
	}
	// Ajouter un doublon (même celui de la config) est sans effet.
	s.AddDomain(Allow, "fixe.example.com")
	if got := s.Domains(Allow); len(got) != 1 || got[0].Source != SourceConfig {
		t.Errorf("Domains = %+v", got)
	}
}

func TestPersistenceAcrossRestart(t *testing.T) {
	s, _, path := newStore(t, nil, nil)
	s.AddDomain(Block, "persiste.example.com")
	s.AddList("Ma liste", "https://exemple.org/hosts.txt")

	eng2 := filter.NewEngine()
	upd2 := filter.NewUpdater(eng2, nil, t.TempDir(), time.Hour)
	s2, err := Open(path, nil, nil, nil, eng2, upd2)
	if err != nil {
		t.Fatal(err)
	}
	if !eng2.Check("persiste.example.com").Blocked {
		t.Error("le domaine doit survivre au redémarrage")
	}
	if l := s2.Lists(); len(l) != 1 || l[0].Name != "Ma liste" || l[0].Source != SourceUI {
		t.Errorf("Lists = %+v", l)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("state.json devrait être en 0600: %v %v", fi, err)
	}
}

func TestLists(t *testing.T) {
	dir := t.TempDir()
	eng := filter.NewEngine()
	upd := filter.NewUpdater(eng, nil, dir, time.Hour)
	s, _ := Open(filepath.Join(dir, "s.json"), nil, nil,
		[]filter.Source{{Name: "Fixe", URL: "https://a.example/list"}}, eng, upd)

	for _, bad := range []string{"", "ftp://x/y", "file:///etc/passwd", "/etc/hosts", "https://"} {
		if err := s.AddList("x", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("AddList(%q) = %v, attendu ErrInvalid", bad, err)
		}
	}
	if err := s.AddList("", "https://b.example/list"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveList("https://a.example/list"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("retirer une liste de config = %v", err)
	}
	if err := s.RemoveList("https://b.example/list"); err != nil {
		t.Fatal(err)
	}
	if got := s.Lists(); len(got) != 1 || got[0].Name != "Fixe" {
		t.Errorf("Lists = %+v", got)
	}
}
