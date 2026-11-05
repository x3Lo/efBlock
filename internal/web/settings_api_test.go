package web

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/x3Lo/efBlock/internal/querylog"
	"github.com/x3Lo/efBlock/internal/settings"
	"github.com/x3Lo/efBlock/internal/store"
)

const me = "192.168.1.5:1234"

func (e *env) put(t *testing.T, c *http.Cookie, group string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return e.do("PUT", "/api/settings/"+group, me, c, body, true)
}

func TestSettingsViewAndUpdate(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	rec := e.do("GET", "/api/settings", me, c, nil, false)
	var v struct {
		Effective  settings.Groups `json:"effective"`
		Overridden []string        `json:"overridden"`
		CanRestart bool            `json:"canRestart"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || rec.Code != 200 {
		t.Fatalf("GET settings: %d %v", rec.Code, err)
	}
	if v.Effective.Upstream.Upstreams[0] != "127.0.0.1:1" || len(v.Overridden) != 0 || !v.CanRestart {
		t.Errorf("vue initiale: %+v", v)
	}

	rec = e.put(t, c, "filter", settings.Filter{BlockMode: "nxdomain", UpdateInterval: "6h"})
	if rec.Code != 200 {
		t.Fatalf("PUT filter: %d %s", rec.Code, rec.Body)
	}
	if e.applied == nil || e.applied.Filter.BlockMode != "nxdomain" || e.applied.Filter.UpdateInterval != 6*time.Hour {
		t.Errorf("le changement doit être mis en application: %+v", e.applied)
	}
	json.Unmarshal(rec.Body.Bytes(), &v)
	if len(v.Overridden) != 1 || v.Overridden[0] != "filter" {
		t.Errorf("overridden = %v", v.Overridden)
	}

	// Rétablir.
	rec = e.do("DELETE", "/api/settings/filter", me, c, nil, true)
	if rec.Code != 200 || e.applied.Filter.BlockMode != "zero_ip" {
		t.Errorf("DELETE: %d, mode=%s", rec.Code, e.applied.Filter.BlockMode)
	}
}

func TestSettingsValidationAndProtection(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	rec := e.put(t, c, "filter", settings.Filter{BlockMode: "boum", UpdateInterval: "24h"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "block_mode") {
		t.Errorf("valeur invalide: %d %s", rec.Code, rec.Body)
	}
	if rec := e.put(t, c, "nimportequoi", struct{}{}); rec.Code != http.StatusBadRequest {
		t.Errorf("groupe inconnu: %d", rec.Code)
	}
	// Sans session et sans en-tête CSRF : refusé comme tout le reste.
	if rec := e.do("PUT", "/api/settings/filter", me, nil, settings.Filter{}, true); rec.Code != 401 {
		t.Errorf("sans session: %d", rec.Code)
	}
	if rec := e.do("PUT", "/api/settings/filter", me, c, settings.Filter{BlockMode: "zero_ip", UpdateInterval: "1h"}, false); rec.Code != 403 {
		t.Errorf("sans en-tête CSRF: %d", rec.Code)
	}
}

// On ne peut pas se couper l'accès à l'interface depuis l'interface.
func TestCannotLockYourselfOut(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)

	rec := e.put(t, c, "access", settings.Access{DNSClients: []string{"192.168.0.0/16"}, WebCIDRs: []string{"10.9.9.0/24"}})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "perdrais l'accès") {
		t.Errorf("réseau excluant l'appelant: %d %s", rec.Code, rec.Body)
	}
	if e.applied != nil {
		t.Error("rien ne doit être appliqué")
	}
	// Un réseau qui inclut l'appelant (192.168.1.5) est accepté et appliqué tout de suite.
	rec = e.put(t, c, "access", settings.Access{DNSClients: []string{"192.168.0.0/16"}, WebCIDRs: []string{"192.168.1.5/32"}})
	if rec.Code != 200 {
		t.Fatalf("accès restreint à soi: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("GET", "/api/session", "192.168.1.6:1", c, nil, false); rec.Code != http.StatusForbidden {
		t.Errorf("un autre poste du LAN doit maintenant être refusé: %d", rec.Code)
	}
	if rec := e.do("GET", "/api/session", me, c, nil, false); rec.Code != 200 {
		t.Errorf("le poste autorisé doit garder l'accès: %d", rec.Code)
	}
}

func TestPasswordChange(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	other := e.login(t) // une 2e session (un autre navigateur)

	// Mauvais mot de passe actuel.
	if rec := e.do("POST", "/api/password", me, c, map[string]string{"current": "faux", "new": "nouveau-mot-de-passe"}, true); rec.Code != http.StatusForbidden {
		t.Errorf("mauvais mot de passe actuel: %d, attendu 403 (pas 401 : la session reste valide)", rec.Code)
	}
	if rec := e.do("GET", "/api/session", me, c, nil, false); rec.Code != 200 {
		t.Error("un mauvais mot de passe actuel ne doit pas fermer la session")
	}
	// Trop court / trop long.
	if rec := e.do("POST", "/api/password", me, c, map[string]string{"current": testPassword, "new": "court"}, true); rec.Code != 400 {
		t.Errorf("trop court: %d", rec.Code)
	}
	if rec := e.do("POST", "/api/password", me, c, map[string]string{"current": testPassword, "new": strings.Repeat("a", 80)}, true); rec.Code != 400 {
		t.Errorf("trop long: %d", rec.Code)
	}

	rec := e.do("POST", "/api/password", me, c, map[string]string{"current": testPassword, "new": "nouveau-mot-de-passe"}, true)
	if rec.Code != 200 {
		t.Fatalf("changement: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("GET", "/api/session", me, c, nil, false); rec.Code != 200 {
		t.Error("la session qui a changé le mot de passe doit rester ouverte")
	}
	if rec := e.do("GET", "/api/session", me, other, nil, false); rec.Code != 401 {
		t.Error("les autres sessions doivent être fermées")
	}
	if rec := e.do("POST", "/api/login", "192.168.1.7:1", nil, map[string]string{"password": testPassword}, true); rec.Code != 401 {
		t.Error("l'ancien mot de passe ne doit plus fonctionner")
	}
	if rec := e.do("POST", "/api/login", "192.168.1.7:1", nil, map[string]string{"password": "nouveau-mot-de-passe"}, true); rec.Code != 200 {
		t.Errorf("le nouveau mot de passe doit fonctionner: %d", rec.Code)
	}
}

// Deviner le mot de passe actuel via le formulaire de changement doit être aussi freiné qu'au login.
func TestPasswordChangeIsRateLimited(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	for i := 0; i < loginAttempts; i++ {
		e.do("POST", "/api/password", me, c, map[string]string{"current": "faux", "new": "nouveau-mot-de-passe"}, true)
	}
	rec := e.do("POST", "/api/password", me, c, map[string]string{"current": testPassword, "new": "nouveau-mot-de-passe"}, true)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("après %d échecs: %d, attendu 429", loginAttempts, rec.Code)
	}
}

func TestRestart(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	if rec := e.do("POST", "/api/restart", me, c, nil, true); rec.Code != http.StatusAccepted {
		t.Fatalf("restart: %d", rec.Code)
	}
	deadline := time.Now().Add(2 * time.Second)
	for e.restarts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if e.restarts.Load() != 1 {
		t.Error("la fonction de redémarrage doit être appelée")
	}

	// Hors systemd (Restart == nil) : refus clair.
	e.srv.d.Restart = nil
	if rec := e.do("POST", "/api/restart", me, c, nil, true); rec.Code != http.StatusConflict {
		t.Errorf("sans systemd: %d, attendu 409", rec.Code)
	}
}

func TestUpstreamsTest(t *testing.T) {
	// Un faux résolveur qui répond, et un port fermé.
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	good := pc.LocalAddr().String()
	pc.Close()
	started := make(chan struct{})
	srv := &dns.Server{Addr: good, Net: "udp", NotifyStartedFunc: func() { close(started) },
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
			resp := new(dns.Msg)
			resp.SetReply(r)
			_ = w.WriteMsg(resp)
		})}
	go srv.ListenAndServe()
	<-started
	defer srv.Shutdown()

	e := newEnv(t)
	c := e.login(t)
	rec := e.do("POST", "/api/upstreams/test", me, c,
		map[string]any{"upstreams": []string{good, "127.0.0.1:1", "ftp://x"}, "timeout": "500ms"}, true)
	var got struct {
		Results []struct {
			Name  string `json:"name"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 || len(got.Results) != 3 {
		t.Fatalf("test upstreams: %d %v %s", rec.Code, err, rec.Body)
	}
	if !got.Results[0].OK {
		t.Errorf("le résolveur qui répond doit être OK: %+v", got.Results[0])
	}
	if got.Results[1].OK || got.Results[1].Error == "" {
		t.Errorf("le port fermé doit échouer avec une explication: %+v", got.Results[1])
	}
	if got.Results[2].OK || !strings.Contains(got.Results[2].Error, "protocole") {
		t.Errorf("une adresse invalide doit être signalée: %+v", got.Results[2])
	}
	if rec := e.do("POST", "/api/upstreams/test", me, c, map[string]any{"upstreams": []string{}}, true); rec.Code != 400 {
		t.Errorf("liste vide: %d", rec.Code)
	}
}

func TestGroupsAPI(t *testing.T) {
	e := newEnv(t)
	c := e.login(t)
	if rec := e.do("GET", "/api/groups", me, nil, nil, false); rec.Code != 401 {
		t.Errorf("sans session: %d", rec.Code)
	}

	// Un client vu dans le journal est proposé.
	e.log.Log(querylog.Entry{Time: time.Now(), Client: "192.168.1.77", Name: "x.example.com", QType: "A", Action: querylog.ActionForwarded, Rcode: "NOERROR"})
	time.Sleep(1300 * time.Millisecond)

	var got struct {
		Groups  []store.Group `json:"groups"`
		Clients []struct {
			IP string `json:"ip"`
		} `json:"clients"`
	}
	rec := e.do("GET", "/api/groups", me, c, nil, false)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("GET: %d %v", rec.Code, err)
	}
	if len(got.Groups) != 0 || len(got.Clients) != 1 || got.Clients[0].IP != "192.168.1.77" {
		t.Errorf("état initial: %+v", got)
	}

	body := map[string]any{"groups": []map[string]any{{
		"name": "Invités", "clients": []string{"192.168.1.77"}, "mode": "off",
		"all_lists": true, "lists": []string{}, "allow": []string{}, "block": []string{},
	}}}
	// Sans l'en-tête anti-CSRF : refusé.
	if rec := e.do("PUT", "/api/groups", me, c, body, false); rec.Code != 403 {
		t.Errorf("sans en-tête CSRF: %d", rec.Code)
	}
	rec = e.do("PUT", "/api/groups", me, c, body, true)
	if rec.Code != 200 {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
	}
	e.engine.SetUserRules(nil, []string{"pub.example.com"})
	if v := e.engine.CheckClient(net.ParseIP("192.168.1.77"), "pub.example.com"); v.Blocked || v.Group != "Invités" {
		t.Errorf("le groupe doit être appliqué tout de suite: %+v", v)
	}

	// Entrée invalide : refus clair, état précédent conservé.
	bad := map[string]any{"groups": []map[string]any{{"name": "X", "clients": []string{"pas-une-ip"}, "mode": "off", "all_lists": true}}}
	rec = e.do("PUT", "/api/groups", me, c, bad, true)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "pas-une-ip") {
		t.Errorf("client invalide: %d %s", rec.Code, rec.Body)
	}
	if v := e.engine.CheckClient(net.ParseIP("192.168.1.77"), "pub.example.com"); v.Group != "Invités" {
		t.Error("après un refus, les groupes précédents doivent rester")
	}
	if rec := e.do("PUT", "/api/groups", me, c, map[string]any{"groupes": []int{}}, true); rec.Code != 400 {
		t.Errorf("champ inconnu: %d", rec.Code)
	}
}
