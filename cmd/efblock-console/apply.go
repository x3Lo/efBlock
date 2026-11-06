package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Mise à jour demandée depuis l'interface web (voir internal/web/update_api.go).
//
// L'unité systemd efblock-update.path lance « efblock-console -apply-update » en root dès que
// l'interface dépose le fichier de demande. On efface la demande AVANT d'agir (sinon l'unité
// se relancerait en boucle), on écrit l'état à chaque étape, et on garde le déroulé dans un journal.
var (
	updateRequestFile = "/var/lib/efblock/update-request"
	updateStatusFile  = "/var/lib/efblock/update-status"
	updateLogFile     = "/var/lib/efblock/update.log"

	runUpdateFn = runUpdate // remplaçable dans les tests

	// L'écran de la machine (efblock-console.service) tourne avec l'ancien programme : on le relance
	// pour qu'il affiche la nouvelle version. Sûr ici : cette mise à jour ne tourne PAS dans cet écran.
	restartConsoleFn = func() { _ = exec.Command("systemctl", "restart", "efblock-console").Run() }
)

// writeUpdateStatus écrit l'état de façon atomique : l'interface ne lit jamais un fichier à moitié écrit.
func writeUpdateStatus(state, msg string) {
	b, _ := json.Marshal(map[string]string{"state": state, "message": msg, "time": time.Now().Format(time.RFC3339)})
	tmp := updateStatusFile + ".tmp"
	if err := os.MkdirAll(filepath.Dir(updateStatusFile), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(tmp, b, 0o644); err == nil {
		_ = os.Rename(tmp, updateStatusFile)
	}
}

// applyUpdate exécute la mise à jour demandée. Retourne le code de sortie du programme.
func applyUpdate() int {
	_ = os.Remove(updateRequestFile)
	writeUpdateStatus("running", "Recherche de la dernière version…")

	logf, err := os.OpenFile(updateLogFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		writeUpdateStatus("failed", "journal illisible : "+err.Error())
		return 1
	}
	defer logf.Close()
	ui := &UI{in: bufio.NewReader(strings.NewReader("")), out: logf}

	if err := runUpdateFn(ui, true); err != nil {
		msg := err.Error()
		if len(msg) > 400 {
			msg = msg[:400] + "…"
		}
		writeUpdateStatus("failed", msg)
		return 1
	}
	writeUpdateStatus("ok", "Mise à jour terminée.")
	restartConsoleFn()
	return 0
}
