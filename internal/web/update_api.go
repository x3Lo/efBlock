package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/x3Lo/efBlock/internal/update"
)

// Mise à jour depuis l'interface.
//
// Le serveur web tourne volontairement SANS droits : il ne peut pas installer de paquet. Il se
// contente de (1) signaler la version disponible et (2) déposer un fichier de demande. Une unité
// systemd (efblock-update.path), en root, voit ce fichier et lance « efblock-console -apply-update »,
// qui télécharge, vérifie l'empreinte et installe, puis écrit l'état dans StatusFile.
// La demande ne contient rien d'utile : la version installée est toujours celle que l'unité
// détermine elle-même auprès de GitHub.

// UpdateConfig règle la mise à jour depuis l'interface.
type UpdateConfig struct {
	Version     string // version installée
	API         string // liste des publications ; vide = GitHub
	RequestFile string // fichier de demande ; vide = mise à jour depuis l'interface indisponible
	StatusFile  string // état écrit par la console (JSON)
}

const (
	updateCheckEvery   = 10 * time.Minute // on ne contacte GitHub qu'une fois par période...
	updateRefreshEvery = 20 * time.Second // ...sauf demande explicite, au plus toutes les 20 s
	updateStaleAfter   = 30 * time.Minute // un état « en cours » plus ancien est abandonné
)

type updateStatus struct {
	State   string `json:"state"` // idle | running | ok | failed
	Message string `json:"message"`
	Time    string `json:"time,omitempty"`
}

type updateInfo struct {
	Current  string       `json:"current"`
	Latest   string       `json:"latest"`
	Newer    bool         `json:"newer"`
	Checked  string       `json:"checked"`
	Error    string       `json:"error"`
	CanApply bool         `json:"can_apply"`
	Reason   string       `json:"reason,omitempty"` // pourquoi on ne peut pas l'appliquer d'ici
	Status   updateStatus `json:"status"`
}

type updateState struct {
	mu      sync.Mutex
	latest  update.Release
	checked time.Time
	err     string
}

// check retourne la dernière version connue, en la redemandant à GitHub si elle est trop ancienne.
func (s *Server) checkUpdate(force bool) (update.Release, time.Time, string) {
	st := &s.upd
	st.mu.Lock()
	defer st.mu.Unlock()
	age := time.Since(st.checked)
	if !st.checked.IsZero() && ((!force && age < updateCheckEvery) || (force && age < updateRefreshEvery)) {
		return st.latest, st.checked, st.err
	}
	api := s.d.Update.API
	if api == "" {
		api = update.DefaultAPI
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rel, err := update.LatestRelease(ctx, &http.Client{Timeout: 15 * time.Second}, api, update.AcceptsPrereleases(s.d.Update.Version))
	st.checked = time.Now()
	if err != nil {
		st.err = "impossible de joindre GitHub : " + err.Error()
		st.latest = update.Release{}
	} else {
		st.err, st.latest = "", rel
	}
	return st.latest, st.checked, st.err
}

// readUpdateStatus lit l'état écrit par la console (jamais d'erreur : absent = au repos).
func (s *Server) readUpdateStatus() updateStatus {
	st := updateStatus{State: "idle"}
	if s.d.Update.StatusFile == "" {
		return st
	}
	data, err := os.ReadFile(s.d.Update.StatusFile)
	if err != nil || json.Unmarshal(data, &st) != nil || st.State == "" {
		return updateStatus{State: "idle"}
	}
	if st.State == "running" {
		if t, err := time.Parse(time.RFC3339, st.Time); err != nil || time.Since(t) > updateStaleAfter {
			return updateStatus{State: "failed", Message: "mise à jour interrompue", Time: st.Time}
		}
	}
	return st
}

// applyBlocker explique pourquoi on ne peut pas lancer la mise à jour d'ici ("" = possible).
func (s *Server) applyBlocker() string {
	if _, ok := update.Semver(s.d.Update.Version); !ok {
		return "version de développement : mise à jour automatique désactivée"
	}
	if s.d.Update.RequestFile == "" {
		return "mise à jour depuis l'interface indisponible : installe efBlock par le paquet Debian ou l'ISO"
	}
	return ""
}

func (s *Server) handleUpdateInfo(w http.ResponseWriter, r *http.Request) {
	rel, checked, errMsg := s.checkUpdate(r.URL.Query().Get("refresh") == "1")
	info := updateInfo{
		Current: s.d.Update.Version,
		Latest:  rel.TagName,
		Newer:   rel.TagName != "" && update.NewerThan(s.d.Update.Version, rel.TagName),
		Error:   errMsg,
		Status:  s.readUpdateStatus(),
	}
	if !checked.IsZero() {
		info.Checked = checked.Format(time.RFC3339)
	}
	info.Reason = s.applyBlocker()
	info.CanApply = info.Reason == ""
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if reason := s.applyBlocker(); reason != "" {
		writeError(w, http.StatusConflict, reason)
		return
	}
	if s.readUpdateStatus().State == "running" {
		writeError(w, http.StatusConflict, "une mise à jour est déjà en cours")
		return
	}
	rel, _, errMsg := s.checkUpdate(false)
	if errMsg != "" {
		writeError(w, http.StatusBadGateway, errMsg)
		return
	}
	if !update.NewerThan(s.d.Update.Version, rel.TagName) {
		writeError(w, http.StatusConflict, "efBlock est déjà à jour")
		return
	}
	if err := os.WriteFile(s.d.Update.RequestFile, []byte(time.Now().Format(time.RFC3339)+"\n"), 0o640); err != nil {
		slog.Error("demande de mise à jour impossible", "err", err)
		writeError(w, http.StatusInternalServerError, "impossible de déposer la demande de mise à jour")
		return
	}
	slog.Info("mise à jour demandée depuis l'interface", "vers", rel.TagName)
	writeJSON(w, http.StatusAccepted, map[string]any{"requested": true, "version": rel.TagName})
}
