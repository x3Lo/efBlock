package web

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/x3Lo/efBlock/internal/settings"
	"github.com/x3Lo/efBlock/internal/upstream"
)

type settingsResponse struct {
	settings.View
	CanRestart bool `json:"canRestart"`
}

func (s *Server) settingsView() settingsResponse {
	return settingsResponse{View: s.d.Settings.View(), CanRestart: s.d.Restart != nil}
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settingsView())
}

// settingsError traduit une erreur du gestionnaire de réglages.
func settingsError(w http.ResponseWriter, err error) {
	if errors.Is(err, settings.ErrInvalid) {
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), settings.ErrInvalid.Error()+": "))
		return
	}
	slog.Error("réglages", "err", err)
	writeError(w, http.StatusInternalServerError, "erreur interne")
}

func (s *Server) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	group := r.PathValue("group")
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "requête invalide")
		return
	}

	// Garde-fou contre l'auto-verrouillage : on refuse une liste de réseaux qui
	// exclurait la machine qui est en train de faire la modification, sinon
	// l'interface deviendrait inaccessible depuis cette machine.
	if group == settings.GroupAccess {
		var a settings.Access
		if json.Unmarshal(raw, &a) == nil && len(a.WebCIDRs) > 0 {
			me := clientIP(r)
			ok := false
			for _, c := range a.WebCIDRs {
				if _, n, err := net.ParseCIDR(strings.TrimSpace(c)); err == nil && n.Contains(me) {
					ok = true
				}
			}
			if !ok {
				writeError(w, http.StatusBadRequest, "ton adresse ("+me.String()+") n'est pas dans ces réseaux : tu perdrais l'accès à l'interface")
				return
			}
		}
	}

	if err := s.d.Settings.Update(group, raw); err != nil {
		settingsError(w, err)
		return
	}
	slog.Info("réglages modifiés depuis l'interface", "groupe", group)
	writeJSON(w, http.StatusOK, s.settingsView())
}

func (s *Server) handleSettingsReset(w http.ResponseWriter, r *http.Request) {
	group := r.PathValue("group")
	if err := s.d.Settings.Reset(group); err != nil {
		settingsError(w, err)
		return
	}
	slog.Info("réglages rétablis depuis l'interface", "groupe", group)
	writeJSON(w, http.StatusOK, s.settingsView())
}

// handleUpstreamsTest essaie réellement chaque upstream saisi, sans rien enregistrer.
func (s *Server) handleUpstreamsTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Upstreams []string `json:"upstreams"`
		Bootstrap []string `json:"bootstrap"`
		Timeout   string   `json:"timeout"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Upstreams) == 0 || len(body.Upstreams) > 10 {
		writeError(w, http.StatusBadRequest, "entre entre 1 et 10 upstreams")
		return
	}
	timeout := 3 * time.Second
	if d, err := time.ParseDuration(body.Timeout); err == nil && d > 0 && d <= 10*time.Second {
		timeout = d
	}
	type result struct {
		Name      string `json:"name"`
		OK        bool   `json:"ok"`
		LatencyMs int64  `json:"latencyMs"`
		Error     string `json:"error,omitempty"`
	}
	out := []result{}
	for _, t := range upstream.Test(body.Upstreams, timeout, body.Bootstrap) {
		out = append(out, result{t.Name, t.OK, t.LatencyMs, t.Error})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

// handlePassword change le mot de passe : il faut connaître l'actuel (un passant devant
// un poste resté ouvert ne doit pas pouvoir le changer d'un clic).
func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, &body) {
		return
	}
	ip := clientIP(r).String()
	if !s.limiter.allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "trop de tentatives, réessaie dans quelques minutes")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(*s.hash.Load()), []byte(body.Current)) != nil {
		s.limiter.failure(ip)
		// 403 et non 401 : la session est valide, c'est la confirmation qui est fausse.
		// Un 401 ferait croire à l'interface que la session a expiré et la déconnecterait.
		writeError(w, http.StatusForbidden, "mot de passe actuel incorrect")
		return
	}
	s.limiter.success(ip)
	if len(body.New) < 8 {
		writeError(w, http.StatusBadRequest, "le nouveau mot de passe doit faire au moins 8 caractères")
		return
	}
	if len(body.New) > 72 { // limite de bcrypt : au-delà, la fin serait ignorée
		writeError(w, http.StatusBadRequest, "le nouveau mot de passe ne peut pas dépasser 72 caractères")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.New), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "erreur interne")
		return
	}
	if err := s.d.Settings.SetPasswordHash(string(hash)); err != nil {
		settingsError(w, err)
		return
	}
	// Les autres sessions ouvertes (un autre navigateur, un autre appareil) sont fermées.
	if c, err := r.Cookie(cookieName); err == nil {
		s.sessions.deleteExcept(c.Value)
	}
	slog.Info("mot de passe de l'interface modifié")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleRestart redémarre efBlock. Il quitte proprement ; c'est systemd qui le relance
// (Restart=always). Sans systemd, personne ne le relancerait : on refuse.
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if s.d.Restart == nil {
		writeError(w, http.StatusConflict, "redémarrage impossible : efBlock ne tourne pas sous systemd")
		return
	}
	slog.Info("redémarrage demandé depuis l'interface")
	writeJSON(w, http.StatusAccepted, map[string]bool{"restarting": true})
	go func() {
		time.Sleep(300 * time.Millisecond) // laisse partir la réponse
		s.d.Restart()
	}()
}
