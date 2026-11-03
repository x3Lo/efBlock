package web

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/x3Lo/efBlock/internal/querylog"
	"github.com/x3Lo/efBlock/internal/store"
)

// --- Aides JSON ------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decode lit un corps JSON (taille limitée, champs inconnus refusés).
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "requête invalide")
		return false
	}
	return true
}

// storeError traduit une erreur du Store en réponse HTTP.
func storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrReadOnly):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrTooMany):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		slog.Error("store", "err", err)
		writeError(w, http.StatusInternalServerError, "erreur interne")
	}
}

// --- Connexion -------------------------------------------------------------

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(csrfHeader) != csrfValue {
		writeError(w, http.StatusForbidden, "en-tête de sécurité manquant")
		return
	}
	ip := clientIP(r).String()
	if !s.limiter.allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "trop de tentatives, réessaie dans quelques minutes")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	// bcrypt est volontairement lent (~50-100 ms) : ça rend le test de
	// milliers de mots de passe impraticable.
	if bcrypt.CompareHashAndPassword([]byte(s.d.PasswordHash), []byte(body.Password)) != nil {
		s.limiter.failure(ip)
		slog.Warn("échec de connexion à l'interface web", "ip", ip)
		writeError(w, http.StatusUnauthorized, "mot de passe incorrect")
		return
	}
	s.limiter.success(ip)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.sessions.create(),
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true, // invisible pour le JavaScript de la page : un script injecté ne peut pas le voler
		SameSite: http.SameSiteStrictMode,
		Secure:   s.d.Secure,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(cookieName)
	if err != nil || !s.sessions.valid(c.Value) {
		writeError(w, http.StatusUnauthorized, "non connecté")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.sessions.delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.d.Secure})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Tableau de bord -------------------------------------------------------

type statsResponse struct {
	LogEnabled bool                              `json:"logEnabled"`
	Hours      int                               `json:"hours"`
	Summary    summaryJSON                       `json:"summary"`
	TopBlocked []querylog.Count                  `json:"topBlocked"`
	TopDomains []querylog.Count                  `json:"topDomains"`
	TopClients []querylog.Count                  `json:"topClients"`
	Timeline   []bucketJSON                      `json:"timeline"`
	Blocking   blockingJSON                      `json:"blocking"`
	Filter     struct{ Blocked, Exceptions int } `json:"filter"`
	Cache      *struct {
		Hits, Misses uint64
		Entries      int
	} `json:"cache"`
}

type summaryJSON struct {
	Total      int64   `json:"total"`
	Blocked    int64   `json:"blocked"`
	Cached     int64   `json:"cached"`
	Forwarded  int64   `json:"forwarded"`
	Errors     int64   `json:"errors"`
	BlockedPct float64 `json:"blockedPct"`
}

type bucketJSON struct {
	Start   time.Time `json:"start"`
	Total   int64     `json:"total"`
	Blocked int64     `json:"blocked"`
}

type blockingJSON struct {
	Mode        string     `json:"mode"` // "on", "off" ou "paused"
	PausedUntil *time.Time `json:"pausedUntil,omitempty"`
}

func (s *Server) blockingState() blockingJSON {
	enabled, until := s.d.Engine.State()
	switch {
	case !enabled:
		return blockingJSON{Mode: "off"}
	case !until.IsZero():
		return blockingJSON{Mode: "paused", PausedUntil: &until}
	}
	return blockingJSON{Mode: "on"}
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	hours := 24
	switch r.URL.Query().Get("hours") {
	case "1":
		hours = 1
	case "168":
		hours = 168
	}
	resp := statsResponse{Hours: hours, Blocking: s.blockingState(),
		TopBlocked: []querylog.Count{}, TopDomains: []querylog.Count{}, TopClients: []querylog.Count{}, Timeline: []bucketJSON{}}
	resp.Filter.Blocked, resp.Filter.Exceptions = s.d.Engine.Counts()
	if s.d.Cache != nil {
		h, m, n := s.d.Cache.Stats()
		resp.Cache = &struct {
			Hits, Misses uint64
			Entries      int
		}{h, m, n}
	}

	if s.d.Log != nil {
		resp.LogEnabled = true
		since := time.Now().Add(-time.Duration(hours) * time.Hour)
		// Largeur des barres du graphique selon la période : ~12 à 28 barres.
		step := map[int]time.Duration{1: 5 * time.Minute, 24: time.Hour, 168: 6 * time.Hour}[hours]

		sum, err := s.d.Log.Summary(since)
		if err == nil {
			resp.Summary = summaryJSON{sum.Total, sum.Blocked, sum.Cached, sum.Forwarded, sum.Errors, sum.BlockedPercent()}
			var e1, e2, e3, e4 error
			if v, e := s.d.Log.TopBlocked(since, 10); e == nil && v != nil {
				resp.TopBlocked = v
			} else {
				e1 = e
			}
			if v, e := s.d.Log.TopDomains(since, 10); e == nil && v != nil {
				resp.TopDomains = v
			} else {
				e2 = e
			}
			if v, e := s.d.Log.TopClients(since, 10); e == nil && v != nil {
				resp.TopClients = v
			} else {
				e3 = e
			}
			if tl, e := s.d.Log.Timeline(since, step); e == nil {
				for _, b := range tl {
					resp.Timeline = append(resp.Timeline, bucketJSON{b.Start, b.Total, b.Blocked})
				}
			} else {
				e4 = e
			}
			err = errors.Join(e1, e2, e3, e4)
		}
		if err != nil {
			slog.Error("statistiques", "err", err)
			writeError(w, http.StatusInternalServerError, "erreur de lecture du journal")
			return
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleBlocking : activer, désactiver, ou mettre en pause le blocage.
func (s *Server) handleBlocking(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action  string `json:"action"` // on | off | pause
		Minutes int    `json:"minutes"`
	}
	if !decode(w, r, &body) {
		return
	}
	switch body.Action {
	case "on":
		s.d.Engine.Resume()
	case "off":
		s.d.Engine.Resume() // efface une éventuelle pause avant de désactiver
		s.d.Engine.SetEnabled(false)
	case "pause":
		if body.Minutes < 1 || body.Minutes > 24*60 {
			writeError(w, http.StatusBadRequest, "durée de pause invalide (1 à 1440 minutes)")
			return
		}
		s.d.Engine.SetEnabled(true)
		s.d.Engine.Pause(time.Duration(body.Minutes) * time.Minute)
	default:
		writeError(w, http.StatusBadRequest, "action inconnue")
		return
	}
	slog.Info("blocage modifié depuis l'interface", "action", body.Action, "minutes", body.Minutes)
	writeJSON(w, http.StatusOK, s.blockingState())
}

// --- Journal ---------------------------------------------------------------

type entryJSON struct {
	Time       time.Time `json:"time"`
	Client     string    `json:"client"`
	Name       string    `json:"name"`
	QType      string    `json:"qtype"`
	Action     string    `json:"action"`
	Rule       string    `json:"rule"`
	Rcode      string    `json:"rcode"`
	DurationUs int64     `json:"durationUs"`
}

func (s *Server) handleQueries(w http.ResponseWriter, r *http.Request) {
	if s.d.Log == nil {
		writeJSON(w, http.StatusOK, map[string]any{"logEnabled": false, "entries": []entryJSON{}})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 500 {
		limit = 100
	}
	search := r.URL.Query().Get("q")
	if len(search) > 100 {
		search = search[:100]
	}
	rows, err := s.d.Log.Recent(limit, search)
	if err != nil {
		slog.Error("journal", "err", err)
		writeError(w, http.StatusInternalServerError, "erreur de lecture du journal")
		return
	}
	out := make([]entryJSON, 0, len(rows))
	for _, e := range rows {
		out = append(out, entryJSON{e.Time, e.Client, e.Name, e.QType, e.Action, e.Rule, e.Rcode, e.Duration.Microseconds()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"logEnabled": true, "entries": out})
}

// --- Allowlist / blocklist --------------------------------------------------

func (s *Server) handleDomains(k store.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"entries": s.d.Store.Domains(k)})
	}
}

func (s *Server) handleAddDomain(k store.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Domain string `json:"domain"`
		}
		if !decode(w, r, &body) {
			return
		}
		if err := s.d.Store.AddDomain(k, body.Domain); err != nil {
			storeError(w, err)
			return
		}
		slog.Info("domaine ajouté depuis l'interface", "liste", k, "domaine", body.Domain)
		writeJSON(w, http.StatusOK, map[string]any{"entries": s.d.Store.Domains(k)})
	}
}

func (s *Server) handleRemoveDomain(k store.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.d.Store.RemoveDomain(k, r.URL.Query().Get("domain")); err != nil {
			storeError(w, err)
			return
		}
		slog.Info("domaine retiré depuis l'interface", "liste", k, "domaine", r.URL.Query().Get("domain"))
		writeJSON(w, http.StatusOK, map[string]any{"entries": s.d.Store.Domains(k)})
	}
}

// --- Listes de filtres ------------------------------------------------------

type listJSON struct {
	Name       string     `json:"name"`
	URL        string     `json:"url"`
	Source     string     `json:"source"`
	Blocked    int        `json:"blocked"`
	Exceptions int        `json:"exceptions"`
	UpdatedAt  *time.Time `json:"updatedAt,omitempty"`
	Error      string     `json:"error,omitempty"`
}

func (s *Server) listsResponse() map[string]any {
	status := map[string]int{}
	st := s.d.Updater.Status()
	for i, x := range st {
		status[x.URL] = i
	}
	out := []listJSON{}
	for _, l := range s.d.Store.Lists() {
		j := listJSON{Name: l.Name, URL: l.URL, Source: l.Source}
		if i, ok := status[l.URL]; ok {
			j.Blocked, j.Exceptions, j.Error = st[i].Blocked, st[i].Exceptions, st[i].Error
			if !st[i].UpdatedAt.IsZero() {
				t := st[i].UpdatedAt
				j.UpdatedAt = &t
			}
		}
		out = append(out, j)
	}
	blocked, exceptions := s.d.Engine.Counts()
	return map[string]any{"lists": out, "totalBlocked": blocked, "totalExceptions": exceptions}
}

func (s *Server) handleLists(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.listsResponse())
}

func (s *Server) handleAddList(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.d.Store.AddList(body.Name, body.URL); err != nil {
		storeError(w, err)
		return
	}
	slog.Info("liste ajoutée depuis l'interface", "url", body.URL)
	writeJSON(w, http.StatusOK, s.listsResponse())
}

func (s *Server) handleRemoveList(w http.ResponseWriter, r *http.Request) {
	if err := s.d.Store.RemoveList(r.URL.Query().Get("url")); err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.listsResponse())
}

// handleUpdateLists force le retéléchargement (asynchrone : la réponse est immédiate).
func (s *Server) handleUpdateLists(w http.ResponseWriter, r *http.Request) {
	s.d.Updater.Trigger()
	slog.Info("mise à jour des listes demandée depuis l'interface")
	writeJSON(w, http.StatusAccepted, map[string]bool{"started": true})
}
