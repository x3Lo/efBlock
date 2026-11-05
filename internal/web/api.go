package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
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
	if bcrypt.CompareHashAndPassword([]byte(*s.hash.Load()), []byte(body.Password)) != nil {
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

// rankJSON : une ligne d'un classement (domaine ou client).
type rankJSON struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
	Label string `json:"label,omitempty"` // nom de la machine, pour les clients
}

type upstreamJSON struct {
	Name      string     `json:"name"`
	Healthy   bool       `json:"healthy"`
	Failures  int        `json:"failures"`
	DownUntil *time.Time `json:"downUntil,omitempty"`
}

// heavyStats : la partie coûteuse à calculer (parcourt le journal) ; elle est gardée quelques secondes.
type heavyStats struct {
	Summary    summaryJSON  `json:"summary"`
	TopBlocked []rankJSON   `json:"topBlocked"`
	TopDomains []rankJSON   `json:"topDomains"`
	TopClients []rankJSON   `json:"topClients"`
	Timeline   []bucketJSON `json:"timeline"`
}

type cachedStats struct {
	at   time.Time
	data heavyStats
}

type statsResponse struct {
	heavyStats
	LogEnabled bool           `json:"logEnabled"`
	Hours      int            `json:"hours"`
	Blocking   blockingJSON   `json:"blocking"`
	Upstreams  []upstreamJSON `json:"upstreams"`
	Filter     struct {
		Blocked    int `json:"blocked"`
		Exceptions int `json:"exceptions"`
	} `json:"filter"`
	Cache *struct {
		Hits    uint64 `json:"hits"`
		Misses  uint64 `json:"misses"`
		Entries int    `json:"entries"`
		Stale   uint64 `json:"stale"` // réponses périmées servies en secours
	} `json:"cache"`
}

type summaryJSON struct {
	Total      int64   `json:"total"`
	Blocked    int64   `json:"blocked"`
	Cached     int64   `json:"cached"`
	Forwarded  int64   `json:"forwarded"`
	Local      int64   `json:"local"`
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

// statsTTL : durée pendant laquelle les statistiques lourdes sont réutilisées. Plusieurs
// onglets ouverts, ou un rafraîchissement automatique, ne refont pas le calcul à chaque fois.
const statsTTL = 5 * time.Second

func (s *Server) heavy(hours int) (heavyStats, error) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock() // un seul calcul à la fois : les appels simultanés attendent puis réutilisent le résultat
	if c, ok := s.statsCache[hours]; ok && time.Since(c.at) < statsTTL {
		return c.data, nil
	}

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	// Largeur des barres du graphique selon la période : ~12 à 28 barres.
	step := map[int]time.Duration{1: 5 * time.Minute, 24: time.Hour, 168: 6 * time.Hour}[hours]

	h := heavyStats{TopBlocked: []rankJSON{}, TopDomains: []rankJSON{}, TopClients: []rankJSON{}, Timeline: []bucketJSON{}}
	sum, err := s.d.Log.Summary(since)
	if err != nil {
		return h, err
	}
	h.Summary = summaryJSON{sum.Total, sum.Blocked, sum.Cached, sum.Forwarded, sum.Local, sum.Errors, sum.BlockedPercent()}

	ranks := func(rows []querylog.Count, err error, withNames bool) ([]rankJSON, error) {
		out := []rankJSON{}
		for _, r := range rows {
			out = append(out, rankJSON{Name: r.Name, Count: r.Count})
		}
		return out, err
	}
	var e1, e2, e3, e4 error
	rows, e := s.d.Log.TopBlocked(since, 10)
	h.TopBlocked, e1 = ranks(rows, e, false)
	rows, e = s.d.Log.TopDomains(since, 10)
	h.TopDomains, e2 = ranks(rows, e, false)
	rows, e = s.d.Log.TopClients(since, 10)
	h.TopClients, e3 = ranks(rows, e, true)
	if tl, e := s.d.Log.Timeline(since, step); e == nil {
		for _, b := range tl {
			h.Timeline = append(h.Timeline, bucketJSON{b.Start, b.Total, b.Blocked})
		}
	} else {
		e4 = e
	}
	if err := errors.Join(e1, e2, e3, e4); err != nil {
		return h, err
	}
	s.statsCache[hours] = cachedStats{at: time.Now(), data: h}
	return h, nil
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	hours := 24
	switch r.URL.Query().Get("hours") {
	case "1":
		hours = 1
	case "168":
		hours = 168
	}
	resp := statsResponse{Hours: hours, Blocking: s.blockingState(), Upstreams: []upstreamJSON{},
		heavyStats: heavyStats{TopBlocked: []rankJSON{}, TopDomains: []rankJSON{}, TopClients: []rankJSON{}, Timeline: []bucketJSON{}}}
	resp.Filter.Blocked, resp.Filter.Exceptions = s.d.Engine.Counts()
	if s.d.Cache != nil {
		if c := s.d.Cache(); c != nil {
			h, m, n := c.Stats()
			resp.Cache = &struct {
				Hits    uint64 `json:"hits"`
				Misses  uint64 `json:"misses"`
				Entries int    `json:"entries"`
				Stale   uint64 `json:"stale"`
			}{h, m, n, c.StaleServed()}
		}
	}
	if s.d.Upstream != nil {
		if f := s.d.Upstream(); f != nil {
			for _, st := range f.Status() {
				u := upstreamJSON{Name: st.Name, Healthy: st.Healthy, Failures: st.Failures}
				if !st.Healthy {
					t := st.DownUntil
					u.DownUntil = &t
				}
				resp.Upstreams = append(resp.Upstreams, u)
			}
		}
	}

	if s.d.Log != nil {
		resp.LogEnabled = true
		h, err := s.heavy(hours)
		if err != nil {
			slog.Error("statistiques", "err", err)
			writeError(w, http.StatusInternalServerError, "erreur de lecture du journal")
			return
		}
		resp.heavyStats = h
		// Les noms des machines changent plus souvent que les chiffres : ajoutés à chaque réponse.
		clients := make([]rankJSON, len(h.TopClients))
		copy(clients, h.TopClients)
		for i := range clients {
			clients[i].Label = s.clientName(clients[i].Name)
		}
		resp.TopClients = clients
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) clientName(ip string) string {
	if s.d.ClientName == nil {
		return ""
	}
	return s.d.ClientName(ip)
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
	ClientName string    `json:"clientName,omitempty"`
	Name       string    `json:"name"`
	QType      string    `json:"qtype"`
	Action     string    `json:"action"`
	Rule       string    `json:"rule"`
	Rcode      string    `json:"rcode"`
	DurationUs int64     `json:"durationUs"`
}

// entry convertit une ligne du journal pour l'interface, avec le nom de la machine si on le connaît.
func (s *Server) entry(e querylog.Entry) entryJSON {
	return entryJSON{e.Time, e.Client, s.clientName(e.Client), e.Name, e.QType, e.Action, e.Rule, e.Rcode, e.Duration.Microseconds()}
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
		out = append(out, s.entry(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"logEnabled": true, "entries": out})
}

// maxStreams limite les flux en direct simultanés (chacun garde une connexion ouverte).
const maxStreams = 20

var activeStreams atomic.Int32

// handleQueryStream envoie les requêtes DNS en direct (Server-Sent Events).
//
// Concept : SSE est un format HTTP très simple pour qu'un serveur pousse des
// messages au navigateur. La réponse ne se termine jamais ; chaque message est
// une ligne "data: {...}" suivie d'une ligne vide. Le navigateur le gère avec
// l'objet EventSource et se reconnecte tout seul en cas de coupure.
func (s *Server) handleQueryStream(w http.ResponseWriter, r *http.Request) {
	if s.d.Log == nil {
		writeError(w, http.StatusNotFound, "journal désactivé")
		return
	}
	if activeStreams.Add(1) > maxStreams {
		activeStreams.Add(-1)
		writeError(w, http.StatusTooManyRequests, "trop de flux ouverts")
		return
	}
	defer activeStreams.Add(-1)

	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // demande aux éventuels proxys de ne pas mettre en tampon
	w.WriteHeader(http.StatusOK)

	// Le serveur HTTP impose un délai d'écriture global (WriteTimeout) qui
	// couperait le flux : on le repousse avant chaque écriture.
	write := func(format string, args ...any) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(15 * time.Second))
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !write(": connecté\n\n") {
		return
	}

	ch, cancel := s.d.Log.Subscribe()
	defer cancel()
	cookie, _ := r.Cookie(cookieName)
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-ch:
			if !ok {
				return // journal fermé
			}
			b, _ := json.Marshal(s.entry(e))
			if !write("data: %s\n\n", b) {
				return
			}
		case <-ping.C:
			// Message "vide" pour garder la connexion vivante, et vérification
			// que la session n'a pas été fermée (déconnexion, expiration).
			if cookie == nil || !s.sessions.valid(cookie.Value) || !write(": ping\n\n") {
				return
			}
		}
	}
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

// --- Groupes de clients -------------------------------------------------------

type knownClient struct {
	IP    string `json:"ip"`
	Label string `json:"label,omitempty"`
	Count int64  `json:"count"`
}

type groupsResponse struct {
	Groups []store.Group `json:"groups"`
	Lists  []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"lists"`
	Clients []knownClient `json:"clients"` // clients vus récemment, pour les ajouter d'un clic
}

func (s *Server) groupsResponse() groupsResponse {
	r := groupsResponse{Groups: s.d.Store.Groups(), Clients: []knownClient{}}
	for _, l := range s.d.Store.Lists() {
		r.Lists = append(r.Lists, struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		}{l.Name, l.URL})
	}
	if s.d.Log != nil {
		if top, err := s.d.Log.TopClients(time.Now().Add(-7*24*time.Hour), 50); err == nil {
			for _, c := range top {
				r.Clients = append(r.Clients, knownClient{IP: c.Name, Label: s.clientName(c.Name), Count: c.Count})
			}
		}
	}
	return r
}

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.groupsResponse())
}

func (s *Server) handleGroupsUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Groups []store.Group `json:"groups"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.d.Store.SetGroups(body.Groups); err != nil {
		storeError(w, err)
		return
	}
	slog.Info("groupes de clients modifiés depuis l'interface", "groupes", len(body.Groups))
	writeJSON(w, http.StatusOK, s.groupsResponse())
}
