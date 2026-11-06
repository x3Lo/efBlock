package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

// firstBootFile : présent une fois la configuration initiale proposée. Elle ne l'est qu'une fois.
var firstBootFile = "/var/lib/efblock/.configuration-initiale"

// sshDropIn : autorise la connexion de root par mot de passe (aucun autre compte n'existe).
var sshDropIn = "/etc/ssh/sshd_config.d/00-efblock.conf"

// firstBoot propose, au premier démarrage, les réglages qui terminent l'installation.
// Chaque question est un simple oui/non ; répondre non à tout est possible : le menu
// principal permet de tout faire plus tard (sauf le serveur SSH, proposé ici seulement).
func firstBoot(ui *UI) {
	if _, err := os.Stat(firstBootFile); err == nil {
		return
	}
	ui.clear()
	ui.printf("Bienvenue dans efBlock\n----------------------\n\nL'installation est terminée. Quelques questions pour finir la configuration.\nRéponds par y (oui) ou n (non), puis Entrée. Entrée seule = la lettre en majuscule.\n\n")

	if ui.confirmYes("Choisir maintenant le mot de passe de l'interface web ?") {
		ui.printf("\n8 caractères au moins.\n\n")
		if setWebPasswordCmd() == nil {
			restartService(ui)
		} else {
			ui.printf("\nMot de passe non modifié : tu pourras le faire avec l'option 2 du menu.\n")
		}
		ui.printf("\n")
	}

	ui.printf("Le serveur SSH permet d'administrer cette machine depuis un autre poste (copier-coller, accès à distance).\n")
	if ui.confirm("Installer le serveur SSH ?") {
		installSSH(ui)
	}

	if ui.eof {
		return // entrée fermée (pas d'écran) : on reposera les questions au prochain démarrage
	}
	if err := os.MkdirAll(filepath.Dir(firstBootFile), 0o755); err == nil {
		_ = os.WriteFile(firstBootFile, []byte("ok\n"), 0o644)
	}
	ui.pause()
}

func setWebPasswordCmd() error {
	cmd := exec.Command(efblockBin, "-set-password", "-config", configPath)
	cmd.Dir = serviceDir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// installSSH installe openssh-server et, si on le veut, autorise root à s'y connecter.
func installSSH(ui *UI) {
	ui.printf("\nInstallation…\n")
	if err := installSSHPackage(); err != nil {
		ui.printf("\nInstallation impossible : %v\n", err)
		return
	}
	ui.printf("\nLe compte administrateur (root) est le seul compte de la machine.\n")
	if !rootHasPassword() {
		if ui.confirmYes("Il n'a pas encore de mot de passe. En choisir un maintenant ?") {
			c := exec.Command("passwd", "root")
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := c.Run(); err != nil {
				ui.printf("\nMot de passe non défini : SSH ne pourra pas servir tant que root n'en a pas (option 7 du menu).\n")
			}
		}
	}
	if ui.confirmYes("Autoriser la connexion de root par SSH, avec son mot de passe ?") {
		if err := os.MkdirAll(filepath.Dir(sshDropIn), 0o755); err == nil {
			err = os.WriteFile(sshDropIn, []byte("PermitRootLogin yes\n"), 0o644)
		}
		_ = exec.Command("systemctl", "enable", "ssh").Run()
		if out, err := exec.Command("systemctl", "restart", "ssh").CombinedOutput(); err != nil {
			ui.printf("Échec du démarrage de SSH : %v\n%s\n", err, out)
			return
		}
		ui.printf("\nSSH est actif. Connexion depuis un autre poste : ssh root@%s\n", firstIPv4(listIfaces()))
		ui.printf("Une fois la machine en production, tu pourras retirer cette autorisation en supprimant %s.\n", sshDropIn)
	}
}

// sshKit : paquets du serveur SSH copiés sur le disque à l'installation (aucun internet nécessaire).
var sshKit = "/usr/share/efblock/kit/ssh"

// installSSHPackage installe openssh-server depuis le kit de l'ISO ; à défaut, depuis internet.
func installSSHPackage() error {
	env := append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	if debs, _ := filepath.Glob(filepath.Join(sshKit, "*.deb")); len(debs) > 0 {
		cmd := exec.Command("dpkg", append([]string{"-i"}, debs...)...)
		cmd.Env, cmd.Stdout, cmd.Stderr = env, os.Stdout, os.Stderr
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	cmd := exec.Command("apt-get", "-y", "install", "openssh-server")
	cmd.Env, cmd.Stdout, cmd.Stderr = env, os.Stdout, os.Stderr
	return cmd.Run()
}
