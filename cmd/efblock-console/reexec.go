package main

import "os"

// Le menu tourne en permanence sur l'écran de la machine. Après une mise à jour, le fichier du
// programme est remplacé sur le disque, mais le programme en mémoire reste l'ancien : il afficherait
// l'ancienne version jusqu'au redémarrage. On repère donc le remplacement du fichier et on se relance.

var (
	exePath  string
	exeStart os.FileInfo
)

// startExe mémorise le fichier exécutable tel qu'il était au démarrage.
func startExe() {
	p, err := os.Executable()
	if err != nil {
		return
	}
	if fi, err := os.Stat(p); err == nil {
		exePath, exeStart = p, fi
	}
}

// binaryReplaced : le fichier à ce chemin n'est plus celui qu'on a lancé (dpkg écrit un nouveau
// fichier puis le renomme : l'identité du fichier change).
func binaryReplaced(path string, start os.FileInfo) bool {
	if path == "" || start == nil {
		return false
	}
	cur, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !os.SameFile(cur, start)
}

// reexecIfReplaced relance le programme si sa version sur le disque a changé.
func reexecIfReplaced() {
	if !binaryReplaced(exePath, exeStart) {
		return
	}
	_ = execSelf(exePath, os.Args, os.Environ()) // en cas d'échec on continue avec l'ancien programme
}
