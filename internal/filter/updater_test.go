package filter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// listServer sert une liste de n domaines, ou du texte brut quand body est non vide.
type listServer struct {
	*httptest.Server
	n    atomic.Int32
	body atomic.Value // string
}

func newListServer(t *testing.T, n int) *listServer {
	t.Helper()
	ls := &listServer{}
	ls.n.Store(int32(n))
	ls.body.Store("")
	ls.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b := ls.body.Load().(string); b != "" {
			fmt.Fprint(w, b) // code 200, mais contenu inattendu
			return
		}
		for i := 0; i < int(ls.n.Load()); i++ {
			fmt.Fprintf(w, "0.0.0.0 d%d.example.com\n", i)
		}
	}))
	t.Cleanup(ls.Close)
	return ls
}

func TestGarbageDownloadKeepsPreviousList(t *testing.T) {
	ls := newListServer(t, 2000)
	dir := t.TempDir()
	e := NewEngine()
	u := NewUpdater(e, []Source{{Name: "x", URL: ls.URL}}, dir, time.Hour)

	if err := u.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := e.Counts(); b != 2000 {
		t.Fatalf("après un bon téléchargement: %d domaines, attendu 2000", b)
	}

	// Le serveur tombe en panne et sert une page d'erreur avec le code 200.
	ls.body.Store("<html><body>Maintenance en cours, réessayez plus tard</body></html>")
	err := u.Update(context.Background())
	if err == nil || !strings.Contains(err.Error(), "aucun domaine reconnu") {
		t.Errorf("erreur = %v, attendu un rejet explicite", err)
	}
	if b, _ := e.Counts(); b != 2000 {
		t.Errorf("après une page d'erreur: %d domaines, attendu 2000 (l'ancienne liste doit rester)", b)
	}
	if st := u.Status(); len(st) != 1 || !strings.Contains(st[0].Error, "rejetée") {
		t.Errorf("l'erreur doit être visible dans le statut: %+v", st)
	}

	// Un redémarrage relit le cache : il doit encore être intact.
	e2 := NewEngine()
	u2 := NewUpdater(e2, []Source{{Name: "x", URL: ls.URL}}, dir, time.Hour)
	if err := u2.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if b, _ := e2.Counts(); b != 2000 {
		t.Errorf("après redémarrage: %d domaines, attendu 2000 (le cache ne doit pas être corrompu)", b)
	}
}

func TestSuddenShrinkIsRejected(t *testing.T) {
	ls := newListServer(t, 1000)
	e := NewEngine()
	u := NewUpdater(e, []Source{{Name: "x", URL: ls.URL}}, t.TempDir(), time.Hour)
	u.Update(context.Background())

	ls.n.Store(100) // 10 % de l'ancienne taille : liste tronquée
	if err := u.Update(context.Background()); err == nil || !strings.Contains(err.Error(), "baisse") {
		t.Errorf("une liste tronquée doit être rejetée, erreur = %v", err)
	}
	if b, _ := e.Counts(); b != 1000 {
		t.Errorf("%d domaines, attendu 1000", b)
	}

	ls.n.Store(800) // baisse raisonnable : acceptée
	if err := u.Update(context.Background()); err != nil {
		t.Errorf("une baisse de 20 %% est normale: %v", err)
	}
	if b, _ := e.Counts(); b != 800 {
		t.Errorf("%d domaines, attendu 800", b)
	}
}

func TestFirstDownloadGarbageLeavesNoCache(t *testing.T) {
	ls := newListServer(t, 0)
	ls.body.Store("<html>erreur</html>")
	dir := t.TempDir()
	e := NewEngine()
	u := NewUpdater(e, []Source{{Name: "x", URL: ls.URL}}, dir, time.Hour)

	if err := u.Update(context.Background()); err == nil {
		t.Error("une erreur était attendue")
	}
	if _, err := countRules(u.cachePath(Source{URL: ls.URL})); err == nil {
		t.Error("aucun fichier de cache ne doit être créé à partir d'une réponse invalide")
	}
}

// Une petite liste personnelle (quelques domaines) reste évidemment acceptée.
func TestSmallListsStayValid(t *testing.T) {
	ls := newListServer(t, 5)
	e := NewEngine()
	u := NewUpdater(e, []Source{{Name: "perso", URL: ls.URL}}, t.TempDir(), time.Hour)
	u.Update(context.Background())
	ls.n.Store(2)
	if err := u.Update(context.Background()); err != nil {
		t.Errorf("une petite liste ne doit pas être comparée à l'ancienne: %v", err)
	}
}
