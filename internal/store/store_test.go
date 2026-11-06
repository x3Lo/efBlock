package store

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
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

// Une adresse complète collée dans le champ est ramenée à son nom de domaine.
func TestAddDomainFromURL(t *testing.T) {
	s, eng, _ := newStore(t, nil, nil)
	if err := s.AddDomain(Block, "https://pub.example.com/banniere.js?x=1"); err != nil {
		t.Fatal(err)
	}
	got := s.Domains(Block)
	if len(got) != 1 || got[0].Domain != "pub.example.com" {
		t.Errorf("enregistré : %+v, attendu pub.example.com", got)
	}
	if v := eng.Check("pub.example.com"); !v.Blocked {
		t.Error("le domaine extrait de l'adresse doit être bloqué")
	}
}

func TestValidationAndErrors(t *testing.T) {
	s, _, _ := newStore(t, []string{"fixe.example.com"}, nil)

	for _, bad := range []string{"", "pas un domaine", "com", "http://192.168.0.1/a", "192.168.0.1"} {
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

// --- Groupes de clients ---

func newStoreWithList(t *testing.T) (*Store, *filter.Engine, string, string) {
	t.Helper()
	dir := t.TempDir()
	eng := filter.NewEngine()
	list := filepath.Join(dir, "liste.txt")
	os.WriteFile(list, []byte("0.0.0.0 pub.example.com\n"), 0o644)
	upd := filter.NewUpdater(eng, []filter.Source{{Name: "L", URL: list}}, dir, time.Hour)
	upd.Rebuild()
	path := filepath.Join(dir, "state.json")
	s, err := Open(path, nil, nil, []filter.Source{{Name: "L", URL: list}}, eng, upd)
	if err != nil {
		t.Fatal(err)
	}
	return s, eng, list, path
}

func TestGroupsAppliedAndPersisted(t *testing.T) {
	s, eng, list, path := newStoreWithList(t)
	err := s.SetGroups([]Group{
		{Name: " Invités ", Clients: []string{"192.168.1.50", "192.168.2.0/24"}, Mode: ModeOff},
		{Name: "Enfants", Clients: []string{"192.168.1.20"}, Mode: ModeCustom, AllLists: false, Lists: []string{list},
			Block: []string{"JEUX.Example.NET."}, Allow: []string{"ecole.example.org"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ip := func(x string) net.IP { return net.ParseIP(x) }

	if v := eng.CheckClient(ip("192.168.2.9"), "pub.example.com"); v.Blocked || v.Group != "Invités" {
		t.Errorf("groupe sans blocage: %+v", v)
	}
	if v := eng.CheckClient(ip("192.168.1.20"), "jeux.example.net"); !v.Blocked || v.Group != "Enfants" {
		t.Errorf("la blocklist du groupe s'applique (domaine normalisé): %+v", v)
	}
	if got := s.Groups(); got[0].Name != "Invités" || got[1].Block[0] != "jeux.example.net" {
		t.Errorf("noms et domaines normalisés attendus: %+v", got)
	}

	// Redémarrage : les groupes reviennent depuis state.json.
	eng2 := filter.NewEngine()
	upd2 := filter.NewUpdater(eng2, []filter.Source{{Name: "L", URL: list}}, t.TempDir(), time.Hour)
	upd2.Rebuild()
	if _, err := Open(path, nil, nil, []filter.Source{{Name: "L", URL: list}}, eng2, upd2); err != nil {
		t.Fatal(err)
	}
	if v := eng2.CheckClient(ip("192.168.1.50"), "pub.example.com"); v.Group != "Invités" {
		t.Errorf("après redémarrage: %+v", v)
	}
}

func TestGroupsValidation(t *testing.T) {
	s, eng, list, _ := newStoreWithList(t)
	good := Group{Name: "Bon", Clients: []string{"192.168.1.5"}, Mode: ModeOff}
	if err := s.SetGroups([]Group{good}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		in   []Group
		want string
	}{
		{"nom vide", []Group{{Name: " ", Clients: []string{"1.2.3.4"}, Mode: ModeOff}}, "nom"},
		{"nom trop long", []Group{{Name: strings.Repeat("a", 41), Clients: []string{"1.2.3.4"}, Mode: ModeOff}}, "nom"},
		{"doublon de nom", []Group{{Name: "A", Clients: []string{"1.2.3.4"}, Mode: ModeOff}, {Name: "a", Clients: []string{"1.2.3.5"}, Mode: ModeOff}}, "s'appellent"},
		{"mode inconnu", []Group{{Name: "A", Clients: []string{"1.2.3.4"}, Mode: "boum"}}, "mode"},
		{"aucun client", []Group{{Name: "A", Mode: ModeOff}}, "aucun client"},
		{"client invalide", []Group{{Name: "A", Clients: []string{"pas-une-ip"}, Mode: ModeOff}}, "pas-une-ip"},
		{"domaine invalide", []Group{{Name: "A", Clients: []string{"1.2.3.4"}, Mode: ModeCustom, AllLists: true, Block: []string{"pas un domaine"}}}, "nom de domaine"},
		{"liste inconnue", []Group{{Name: "A", Clients: []string{"1.2.3.4"}, Mode: ModeCustom, Lists: []string{"https://inconnue/x"}}}, "liste inconnue"},
	}
	for _, tt := range tests {
		err := s.SetGroups(tt.in)
		if err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s : erreur = %v, attendu ErrInvalid contenant %q", tt.name, err, tt.want)
		}
	}
	// Trop de groupes.
	many := make([]Group, 31)
	for i := range many {
		many[i] = Group{Name: strings.Repeat("g", 1) + string(rune('a'+i%26)) + string(rune('a'+i/26)), Clients: []string{"1.2.3.4"}, Mode: ModeOff}
	}
	if err := s.SetGroups(many); !errors.Is(err, ErrTooMany) {
		t.Errorf("31 groupes: %v", err)
	}

	// Après tous ces refus, l'état précédent est intact.
	if got := s.Groups(); len(got) != 1 || got[0].Name != "Bon" {
		t.Errorf("les groupes précédents doivent rester: %+v", got)
	}
	if v := eng.CheckClient(net.ParseIP("192.168.1.5"), "pub.example.com"); v.Group != "Bon" {
		t.Error("le moteur doit garder les groupes précédents")
	}
	_ = list
}

func TestRemovingAListPrunesGroups(t *testing.T) {
	s, _, list, _ := newStoreWithList(t)
	if err := s.AddList("Extra", "https://exemple.org/extra.txt"); err != nil {
		t.Fatal(err)
	}
	err := s.SetGroups([]Group{{Name: "G", Clients: []string{"1.2.3.4"}, Mode: ModeCustom, Lists: []string{list, "https://exemple.org/extra.txt"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveList("https://exemple.org/extra.txt"); err != nil {
		t.Fatal(err)
	}
	got := s.Groups()
	if len(got[0].Lists) != 1 || got[0].Lists[0] != list {
		t.Errorf("la liste supprimée doit disparaître des groupes: %v", got[0].Lists)
	}
}
