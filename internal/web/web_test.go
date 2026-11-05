package web

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/x3Lo/efBlock/internal/config"
	"github.com/x3Lo/efBlock/internal/filter"
	"github.com/x3Lo/efBlock/internal/querylog"
	"github.com/x3Lo/efBlock/internal/settings"
	"github.com/x3Lo/efBlock/internal/store"
)

const testPassword = "mot-de-passe-de-test"

type env struct {
	srv      *Server
	h        http.Handler
	engine   *filter.Engine
	log      *querylog.Logger
	settings *settings.Manager
	applied  *config.Config // dernière configuration mise en application
	restarts atomic.Int32
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	eng := filter.NewEngine()
	upd := filter.NewUpdater(eng, nil, dir, time.Hour)
	st, err := store.Open(filepath.Join(dir, "state.json"), nil, nil, nil, eng, upd)
	if err != nil {
		t.Fatal(err)
	}
	ql, err := querylog.Open(filepath.Join(dir, "q.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ql.Close() })

	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	_, lan, _ := net.ParseCIDR("192.168.0.0/16")
	_, lo, _ := net.ParseCIDR("127.0.0.0/8")

	// Réglages : une vraie configuration de base, comme au démarrage d'efBlock.
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := "dns:\n  upstreams: [\"127.0.0.1:1\"]\nweb:\n  allowed_cidrs: [\"192.168.0.0/16\", \"127.0.0.0/8\"]\n  password_hash: '" + string(hash) + "'\n"
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	sm, err := settings.Open(filepath.Join(dir, "settings.json"), base)
	if err != nil {
		t.Fatal(err)
	}

	e := &env{engine: eng, log: ql, settings: sm}
	s := New(Deps{Engine: eng, Updater: upd, Store: st, Log: ql, Settings: sm,
		PasswordHash: string(hash), AllowedNets: []*net.IPNet{lan, lo},
		Restart: func() { e.restarts.Add(1) }})
	// Comme dans main : une configuration validée est mise en application.
	sm.SetApply(func(c *config.Config) error {
		e.applied = c
		s.SetAllowedNets(c.Web.AllowedNets)
		s.SetPasswordHash(c.Web.PasswordHash)
		return nil
	})
	e.srv, e.h = s, s.Handler()
	return e
}

// do envoie une requête depuis remote (ex "192.168.1.5:1234") avec cookie et corps optionnels.
func (e *env) do(method, path, remote string, cookie *http.Cookie, body any, csrf bool) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.RemoteAddr = remote
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf {
		req.Header.Set(csrfHeader, csrfValue)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) login(t *testing.T) *http.Cookie {
	t.Helper()
	rec := e.do("POST", "/api/login", "192.168.1.5:1111", nil, map[string]string{"password": testPassword}, true)
	if rec.Code != 200 {
		t.Fatalf("connexion: %d %s", rec.Code, rec.Body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	t.Fatal("pas de cookie de session")
	return nil
}

func TestOnlyAllowedNetworks(t *testing.T) {
	e := newEnv(t)
	for _, remote := range []string{"8.8.8.8:1234", "10.0.0.5:1234", "[2001:db8::1]:1234"} {
		for _, path := range []string{"/", "/api/session", "/api/login"} {
			if rec := e.do("GET", path, remote, nil, nil, false); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s = %d, attendu 403", remote, path, rec.Code)
			}
		}
	}
	if rec := e.do("GET", "/", "192.168.1.5:1234", nil, nil, false); rec.Code != 200 {
		t.Errorf("une IP autorisée devrait voir la page: %d", rec.Code)
	}
	// IPv4 présentée en IPv6 ("::ffff:192.168.1.5") : doit être reconnue comme 192.168.1.5.
	if rec := e.do("GET", "/", "[::ffff:192.168.1.5]:1234", nil, nil, false); rec.Code != 200 {
		t.Errorf("IPv4-mappée refusée: %d", rec.Code)
	}
	// Un en-tête X-Forwarded-For ne doit PAS permettre de se faire passer pour une IP du LAN.
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "8.8.8.8:1"
	req.Header.Set("X-Forwarded-For", "192.168.1.5")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("X-Forwarded-For falsifié accepté: %d", rec.Code)
	}
}

func TestAPIRequiresLogin(t *testing.T) {
	e := newEnv(t)
	for _, tc := range [][2]string{
		{"GET", "/api/stats"}, {"GET", "/api/queries"}, {"GET", "/api/allowlist"}, {"GET", "/api/blocklist"},
		{"GET", "/api/lists"}, {"POST", "/api/blocking"}, {"POST", "/api/lists/update"}, {"POST", "/api/logout"},
	} {
		rec := e.do(tc[0], tc[1], "192.168.1.5:1", nil, nil, true)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s sans connexion = %d, attendu 401", tc[0], tc[1], rec.Code)
		}
	}
	bad := &http.Cookie{Name: cookieName, Value: "jeton-invente"}
	if rec := e.do("GET", "/api/stats", "192.168.1.5:1", bad, nil, false); rec.Code != 401 {
		t.Errorf("faux cookie accepté: %d", rec.Code)
	}
}

func TestLoginFlow(t *testing.T) {
	e := newEnv(t)

	// Mauvais mot de passe.
	if rec := e.do("POST", "/api/login", "192.168.1.5:1", nil, map[string]string{"password": "faux"}, true); rec.Code != 401 {
		t.Errorf("mauvais mot de passe: %d", rec.Code)
	}
	// Sans l'en-tête anti-CSRF, même le bon mot de passe est refusé.
	if rec := e.do("POST", "/api/login", "192.168.1.5:1", nil, map[string]string{"password": testPassword}, false); rec.Code != 403 {
		t.Errorf("login sans en-tête CSRF: %d, attendu 403", rec.Code)
	}

	c := e.login(t)
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("le cookie doit être HttpOnly et SameSite=Strict: %+v", c)
	}
	if rec := e.do("GET", "/api/session", "192.168.1.5:1", c, nil, false); rec.Code != 200 {
		t.Errorf("session valide refusée: %d", rec.Code)
	}
	if rec := e.do("GET", "/api/stats", "192.168.1.5:1", c, nil, false); rec.Code != 200 {
		t.Errorf("stats: %d %s", rec.Code, rec.Body)
	}

	// Déconnexion : le même cookie ne marche plus.
	if rec := e.do("POST", "/api/logout", "192.168.1.5:1", c, nil, true); rec.Code != 200 {
		t.Errorf("logout: %d", rec.Code)
	}
	if rec := e.do("GET", "/api/stats", "192.168.1.5:1", c, nil, false); rec.Code != 401 {
		t.Errorf("session encore valide après déconnexion: %d", rec.Code)
	}
}

func TestBruteForceLockout(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < loginAttempts; i++ {
		e.do("POST", "/api/login", "192.168.1.9:1", nil, map[string]string{"password": "faux"}, true)
	}
	// Même avec le BON mot de passe, l'IP est maintenant bloquée.
	rec := e.do("POST", "/api/login", "192.168.1.9:1", nil, map[string]string{"password": testPassword}, true)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("après %d échecs: %d, attendu 429", loginAttempts, rec.Code)
	}
	// Une autre IP n'est pas affectée.
	if rec := e.do("POST", "/api/login", "192.168.1.10:1", nil, map[string]string{"password": testPassword}, true); rec.Code != 200 {
		t.Errorf("une autre IP ne doit pas être bloquée: %d", rec.Code)
	}
}

func TestCSRFHeaderRequiredForChanges(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	rec := e.do("POST", "/api/allowlist", "192.168.1.5:1", c, map[string]string{"domain": "ok.example.com"}, false)
	if rec.Code != http.StatusForbidden {
		t.Errorf("modification sans en-tête CSRF: %d, attendu 403", rec.Code)
	}
	if e.engine.Check("ok.example.com").Reason == filter.ReasonUserAllow {
		t.Error("la modification refusée a quand même été appliquée")
	}
}

func TestDomainListsAPI(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	r := "192.168.1.5:1"

	if rec := e.do("POST", "/api/blocklist", r, c, map[string]string{"domain": "pub.example.com"}, true); rec.Code != 200 {
		t.Fatalf("ajout: %d %s", rec.Code, rec.Body)
	}
	if !e.engine.Check("x.pub.example.com").Blocked {
		t.Error("le domaine ajouté via l'API doit être bloqué tout de suite")
	}
	if rec := e.do("POST", "/api/allowlist", r, c, map[string]string{"domain": "pub.example.com"}, true); rec.Code != 200 {
		t.Fatalf("ajout allowlist: %d", rec.Code)
	}
	if e.engine.Check("pub.example.com").Blocked {
		t.Error("l'allowlist doit l'emporter")
	}

	// Entrées invalides, champs inconnus, route inconnue.
	if rec := e.do("POST", "/api/blocklist", r, c, map[string]string{"domain": "n'importe quoi"}, true); rec.Code != 400 {
		t.Errorf("domaine invalide: %d, attendu 400", rec.Code)
	}
	if rec := e.do("POST", "/api/blocklist", r, c, map[string]string{"domaine": "x.example.com"}, true); rec.Code != 400 {
		t.Errorf("champ inconnu: %d, attendu 400", rec.Code)
	}
	if rec := e.do("GET", "/api/nimportequoi", r, c, nil, false); rec.Code != 404 {
		t.Errorf("route inconnue: %d, attendu 404", rec.Code)
	}

	var got struct {
		Entries []store.DomainEntry `json:"entries"`
	}
	rec := e.do("DELETE", "/api/blocklist?domain=pub.example.com", r, c, nil, true)
	json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != 200 || len(got.Entries) != 0 {
		t.Errorf("suppression: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("DELETE", "/api/blocklist?domain=pub.example.com", r, c, nil, true); rec.Code != 404 {
		t.Errorf("supprimer deux fois: %d, attendu 404", rec.Code)
	}
}

func TestBlockingControl(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	r := "192.168.1.5:1"
	e.engine.SetUserRules(nil, []string{"pub.example.com"})

	mode := func(rec *httptest.ResponseRecorder) string {
		var b blockingJSON
		json.Unmarshal(rec.Body.Bytes(), &b)
		return b.Mode
	}

	rec := e.do("POST", "/api/blocking", r, c, map[string]any{"action": "pause", "minutes": 5}, true)
	if rec.Code != 200 || mode(rec) != "paused" || e.engine.Check("pub.example.com").Blocked {
		t.Errorf("pause: %d %s", rec.Code, rec.Body)
	}
	rec = e.do("POST", "/api/blocking", r, c, map[string]any{"action": "on"}, true)
	if mode(rec) != "on" || !e.engine.Check("pub.example.com").Blocked {
		t.Errorf("reprise: %s", rec.Body)
	}
	rec = e.do("POST", "/api/blocking", r, c, map[string]any{"action": "off"}, true)
	if mode(rec) != "off" || e.engine.Check("pub.example.com").Blocked {
		t.Errorf("désactivation: %s", rec.Body)
	}
	for _, bad := range []map[string]any{{"action": "pause", "minutes": 0}, {"action": "pause", "minutes": 99999}, {"action": "boum"}} {
		if rec := e.do("POST", "/api/blocking", r, c, bad, true); rec.Code != 400 {
			t.Errorf("%v: %d, attendu 400", bad, rec.Code)
		}
	}
}

func TestStatsAndQueriesAPI(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	e.log.Log(querylog.Entry{Time: time.Now(), Client: "192.168.50.20", Name: "ads.example.com", QType: "A", Action: querylog.ActionBlocked, Rule: "ads.example.com", Rcode: "NOERROR"})
	e.log.Log(querylog.Entry{Time: time.Now(), Client: "192.168.50.20", Name: "example.org", QType: "A", Action: querylog.ActionForwarded, Rcode: "NOERROR"})
	time.Sleep(1300 * time.Millisecond) // l'écriture du journal est groupée (toutes les secondes)

	rec := e.do("GET", "/api/stats?hours=24", "192.168.1.5:1", c, nil, false)
	var st statsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || rec.Code != 200 {
		t.Fatalf("stats: %d %v %s", rec.Code, err, rec.Body)
	}
	if st.Summary.Total != 2 || st.Summary.Blocked != 1 || st.Summary.BlockedPct != 50 {
		t.Errorf("summary = %+v", st.Summary)
	}
	if len(st.TopBlocked) != 1 || st.TopBlocked[0].Name != "ads.example.com" || len(st.TopClients) != 1 || len(st.Timeline) == 0 {
		t.Errorf("tops/timeline incorrects: %+v", st)
	}

	rec = e.do("GET", "/api/queries?q=ads", "192.168.1.5:1", c, nil, false)
	var qs struct {
		Entries []entryJSON `json:"entries"`
	}
	json.Unmarshal(rec.Body.Bytes(), &qs)
	if len(qs.Entries) != 1 || qs.Entries[0].Name != "ads.example.com" || qs.Entries[0].Rule != "ads.example.com" {
		t.Errorf("queries = %s", rec.Body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	rec := e.do("GET", "/", "192.168.1.5:1", nil, nil, false)
	h := rec.Header()
	if h.Get("X-Frame-Options") != "DENY" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("en-têtes de sécurité absents: %v", h)
	}
	if !strings.Contains(rec.Body.String(), "efBlock") {
		t.Error("la page d'accueil devrait venir de l'interface embarquée")
	}
	rec = e.do("GET", "/api/session", "192.168.1.5:1", nil, nil, false)
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("les réponses de l'API ne doivent pas être mises en cache")
	}
}

// Le flux en direct exige une session, puis envoie chaque requête dès qu'elle est enregistrée.
func TestLiveStream(t *testing.T) {
	e := newEnv(t)
	ts := httptest.NewServer(e.h)
	defer ts.Close()

	// Sans session : refusé.
	resp, err := http.Get(ts.URL + "/api/queries/stream")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("flux sans session: %d, attendu 401", resp.StatusCode)
	}

	// Avec session.
	req, _ := http.NewRequest("GET", ts.URL+"/api/queries/stream", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: e.srv.sessions.create()})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("flux: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	time.Sleep(100 * time.Millisecond) // laisse le temps à l'abonnement de se faire
	e.log.Log(querylog.Entry{Time: time.Now(), Client: "192.168.50.20", Name: "direct.example.com", QType: "A", Action: querylog.ActionBlocked, Rule: "example.com", Rcode: "NOERROR"})

	deadline := time.After(2 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("flux fermé avant d'avoir reçu l'entrée")
			}
			if !strings.HasPrefix(line, "data: ") {
				continue // commentaire ": connecté"
			}
			var got entryJSON
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &got); err != nil {
				t.Fatal(err)
			}
			if got.Name != "direct.example.com" || got.Action != "blocked" || got.Rule != "example.com" {
				t.Errorf("entrée reçue: %+v", got)
			}
			return
		case <-deadline:
			t.Fatal("aucune entrée reçue en direct")
		}
	}
}

// La page de test du blocage est un fichier statique : elle doit s'ouvrir sans connexion (pour
// comparer plusieurs appareils), mais seulement depuis un réseau autorisé.
func TestBlockTestPageIsPublicOnTheLANOnly(t *testing.T) {
	e := newEnv(t)
	rec := e.do("GET", "/test.html", "192.168.1.5:1234", nil, nil, false)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Test du blocage") {
		t.Fatalf("/test.html depuis le LAN : %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "doubleclick") {
		t.Error("la page doit contenir sa liste de domaines de test")
	}
	if rec := e.do("GET", "/test.html", "8.8.8.8:1234", nil, nil, false); rec.Code != http.StatusForbidden {
		t.Errorf("/test.html depuis internet : %d, attendu 403", rec.Code)
	}
}
