package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/x3Lo/efBlock/internal/settings"
	"github.com/x3Lo/efBlock/internal/store"
)

// Sauvegarde et restauration de la configuration faite depuis l'interface : réglages, listes,
// domaines autorisés et bloqués, groupes. Le mot de passe, les adresses d'écoute et le HTTPS
// restent ceux de la machine (les restaurer sur une autre machine pourrait couper l'accès).

const (
	backupFormat   = "efblock-sauvegarde"
	backupVersion  = 1
	maxBackupBytes = 4 << 20
)

type backupFile struct {
	Format   string             `json:"format"`
	Version  int                `json:"version"`
	Created  string             `json:"created"`
	EfBlock  string             `json:"efblock_version"`
	Settings settings.Overrides `json:"settings"`
	State    store.Backup       `json:"state"`
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	b := backupFile{Format: backupFormat, Version: backupVersion, Created: time.Now().Format(time.RFC3339), EfBlock: s.d.Update.Version}
	if s.d.Settings != nil {
		b.Settings = s.d.Settings.Backup()
	}
	b.State = s.d.Store.Backup()
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sauvegarde impossible")
		return
	}
	name := fmt.Sprintf("efblock-sauvegarde-%s.json", time.Now().Format("2006-01-02-1504"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBackupBytes)
	var b backupFile
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "fichier illisible : ce n'est pas une sauvegarde d'efBlock")
		return
	}
	if b.Format != backupFormat {
		writeError(w, http.StatusBadRequest, "ce fichier n'est pas une sauvegarde d'efBlock")
		return
	}
	if b.Version != backupVersion {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("sauvegarde au format %d, non pris en charge par cette version d'efBlock", b.Version))
		return
	}

	// 1. Réglages vérifiés sans rien appliquer ; 2. listes et groupes remplacés (vérifiés d'abord) ;
	// 3. réglages appliqués. Si l'étape 3 échoue malgré tout, on remet les listes et groupes d'avant.
	if s.d.Settings != nil {
		if err := s.d.Settings.CheckRestore(b.Settings); err != nil {
			restoreError(w, err)
			return
		}
	}
	before := s.d.Store.Backup()
	if err := s.d.Store.Restore(b.State); err != nil {
		restoreError(w, err)
		return
	}
	if s.d.Settings != nil {
		if err := s.d.Settings.Restore(b.Settings); err != nil {
			if rerr := s.d.Store.Restore(before); rerr != nil {
				slog.Error("restauration : retour à l'état précédent impossible", "err", rerr)
			}
			restoreError(w, err)
			return
		}
	}
	slog.Info("configuration restaurée depuis une sauvegarde", "créée", b.Created, "version", b.EfBlock)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "lists": len(b.State.Lists), "groups": len(b.State.Groups),
		"allowlist": len(b.State.Allowlist), "blocklist": len(b.State.Blocklist),
	})
}

func restoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, settings.ErrInvalid), errors.Is(err, store.ErrInvalid), errors.Is(err, store.ErrTooMany):
		writeError(w, http.StatusBadRequest, "sauvegarde refusée, rien n'a été changé : "+err.Error())
	default:
		slog.Error("restauration", "err", err)
		writeError(w, http.StatusInternalServerError, "restauration impossible, rien n'a été changé")
	}
}
