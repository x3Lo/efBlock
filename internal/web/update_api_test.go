package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const fakeReleases = `[
 {"tag_name":"v0.1.7","draft":false,"prerelease":true,"assets":[{"name":"efblock_0.1.7_amd64.deb","browser_download_url":"x"},{"name":"SHA256SUMS","browser_download_url":"y"}]},
 {"tag_name":"v0.1.6","draft":false,"prerelease":true,"assets":[{"name":"efblock_0.1.6_amd64.deb","browser_download_url":"x"},{"name":"SHA256SUMS","browser_download_url":"y"}]}]`

// updateEnv : un serveur de test dont la « page des publications » est factice.
func updateEnv(t *testing.T, version, body string, code int) (*env, *http.Cookie, UpdateConfig) {
	t.Helper()
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		w.Write([]byte(body))
	}))
	t.Cleanup(gh.Close)
	dir := t.TempDir()
	cfg := UpdateConfig{Version: version, API: gh.URL, RequestFile: filepath.Join(dir, "demande"), StatusFile: filepath.Join(dir, "etat")}
	e := newEnv(t)
	e.srv.d.Update = cfg
	return e, e.login(t), cfg
}

func getInfo(t *testing.T, e *env, c *http.Cookie, query string) updateInfo {
	t.Helper()
	rec := e.do("GET", "/api/update"+query, "192.168.1.5:1111", c, nil, false)
	if rec.Code != 200 {
		t.Fatalf("GET /api/update : %d %s", rec.Code, rec.Body)
	}
	var info updateInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	return info
}

func TestUpdateRequiresLogin(t *testing.T) {
	e, _, _ := updateEnv(t, "0.1.6", fakeReleases, 200)
	if rec := e.do("GET", "/api/update", "192.168.1.5:1111", nil, nil, false); rec.Code != 401 {
		t.Errorf("GET sans connexion : %d, attendu 401", rec.Code)
	}
	if rec := e.do("POST", "/api/update", "192.168.1.5:1111", nil, nil, true); rec.Code != 401 {
		t.Errorf("POST sans connexion : %d, attendu 401", rec.Code)
	}
}

func TestUpdateCSRF(t *testing.T) {
	e, c, cfg := updateEnv(t, "0.1.6", fakeReleases, 200)
	if rec := e.do("POST", "/api/update", "192.168.1.5:1111", c, nil, false); rec.Code != 403 {
		t.Errorf("POST sans en-tête anti-CSRF : %d, attendu 403", rec.Code)
	}
	if _, err := os.Stat(cfg.RequestFile); err == nil {
		t.Error("une demande a été déposée sans en-tête anti-CSRF")
	}
}

func TestUpdateAvailableAndRequested(t *testing.T) {
	e, c, cfg := updateEnv(t, "0.1.6", fakeReleases, 200)
	info := getInfo(t, e, c, "")
	if info.Latest != "v0.1.7" || !info.Newer || !info.CanApply || info.Current != "0.1.6" || info.Status.State != "idle" {
		t.Fatalf("info inattendue : %+v", info)
	}
	rec := e.do("POST", "/api/update", "192.168.1.5:1111", c, nil, true)
	if rec.Code != 202 {
		t.Fatalf("POST : %d %s, attendu 202", rec.Code, rec.Body)
	}
	if _, err := os.Stat(cfg.RequestFile); err != nil {
		t.Errorf("fichier de demande absent : %v", err)
	}
}

func TestUpdateAlreadyUpToDate(t *testing.T) {
	e, c, cfg := updateEnv(t, "0.1.7", fakeReleases, 200)
	if info := getInfo(t, e, c, ""); info.Newer {
		t.Errorf("0.1.7 face à v0.1.7 ne doit pas proposer de mise à jour : %+v", info)
	}
	if rec := e.do("POST", "/api/update", "192.168.1.5:1111", c, nil, true); rec.Code != 409 {
		t.Errorf("POST alors qu'on est à jour : %d, attendu 409", rec.Code)
	}
	if _, err := os.Stat(cfg.RequestFile); err == nil {
		t.Error("demande déposée alors qu'on est à jour")
	}
}

func TestUpdateNotAvailableWithoutPackageInstall(t *testing.T) {
	e, c, _ := updateEnv(t, "0.1.6", fakeReleases, 200)
	e.srv.d.Update.RequestFile = "" // installation depuis les sources : pas d'unité systemd
	if info := getInfo(t, e, c, ""); info.CanApply || info.Reason == "" {
		t.Errorf("la mise à jour ne doit pas être possible sans unité systemd : %+v", info)
	}
	if rec := e.do("POST", "/api/update", "192.168.1.5:1111", c, nil, true); rec.Code != 409 {
		t.Errorf("POST : %d, attendu 409", rec.Code)
	}
}

func TestUpdateDisabledForDevBuild(t *testing.T) {
	e, c, cfg := updateEnv(t, "dev", fakeReleases, 200)
	if info := getInfo(t, e, c, ""); info.CanApply {
		t.Errorf("une version de développement ne se met pas à jour toute seule : %+v", info)
	}
	if rec := e.do("POST", "/api/update", "192.168.1.5:1111", c, nil, true); rec.Code != 409 {
		t.Errorf("POST : %d, attendu 409", rec.Code)
	}
	if _, err := os.Stat(cfg.RequestFile); err == nil {
		t.Error("demande déposée pour une version de développement")
	}
}

func TestUpdateGitHubUnreachable(t *testing.T) {
	e, c, cfg := updateEnv(t, "0.1.6", "erreur", 500)
	info := getInfo(t, e, c, "")
	if info.Error == "" || info.Newer {
		t.Errorf("une erreur GitHub doit être signalée sans proposer de mise à jour : %+v", info)
	}
	if rec := e.do("POST", "/api/update", "192.168.1.5:1111", c, nil, true); rec.Code != 502 {
		t.Errorf("POST : %d, attendu 502", rec.Code)
	}
	if _, err := os.Stat(cfg.RequestFile); err == nil {
		t.Error("demande déposée alors que GitHub est injoignable")
	}
}

func TestUpdateStatus(t *testing.T) {
	e, c, cfg := updateEnv(t, "0.1.6", fakeReleases, 200)
	write := func(st updateStatus) {
		b, _ := json.Marshal(st)
		os.WriteFile(cfg.StatusFile, b, 0o644)
	}

	// En cours : aucune seconde demande n'est acceptée.
	write(updateStatus{State: "running", Message: "téléchargement", Time: time.Now().Format(time.RFC3339)})
	if info := getInfo(t, e, c, ""); info.Status.State != "running" {
		t.Errorf("état attendu running : %+v", info.Status)
	}
	if rec := e.do("POST", "/api/update", "192.168.1.5:1111", c, nil, true); rec.Code != 409 {
		t.Errorf("POST pendant une mise à jour : %d, attendu 409", rec.Code)
	}

	// Un état « en cours » vieux de plusieurs heures est une mise à jour morte, pas en cours.
	write(updateStatus{State: "running", Time: time.Now().Add(-3 * time.Hour).Format(time.RFC3339)})
	if info := getInfo(t, e, c, ""); info.Status.State != "failed" {
		t.Errorf("une mise à jour figée doit apparaître comme échouée : %+v", info.Status)
	}

	// Terminée, ou échouée : le message est relayé.
	write(updateStatus{State: "failed", Message: "empreinte incorrecte"})
	if info := getInfo(t, e, c, ""); info.Status.State != "failed" || info.Status.Message != "empreinte incorrecte" {
		t.Errorf("état échoué attendu : %+v", info.Status)
	}

	// Fichier illisible : au repos, pas d'erreur.
	os.WriteFile(cfg.StatusFile, []byte("pas du json"), 0o644)
	if info := getInfo(t, e, c, ""); info.Status.State != "idle" {
		t.Errorf("un état illisible doit être ignoré : %+v", info.Status)
	}
}
