// Commande efblock-console : menu d'administration sur la console de la machine.
//
// Il sert à tout régler sans jamais ouvrir un fichier de /etc : réseau, mot de passe de
// l'interface web, adresses d'écoute, HTTPS, redémarrage, journaux, mise à jour.
// Il s'affiche sur l'écran de la machine (tty1) au démarrage, comme la console de pfSense.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/x3Lo/efBlock/internal/config"
	"github.com/x3Lo/efBlock/internal/settings"
	// Certificats racine intégrés : utilisés seulement si le système n'en a pas (ISO sans internet).
	_ "golang.org/x/crypto/x509roots/fallback"
)

// version est renseignée à la compilation (-ldflags "-X main.version=1.2.3").
var version = "dev"

// Chemins du système (variables pour pouvoir les remplacer dans les tests).
var (
	configPath   = "/etc/efblock/config.yaml"
	networkdFile = "/etc/systemd/network/10-efblock.network"
	certDir      = "/etc/efblock"
	efblockBin   = "/usr/bin/efblock"
	serviceDir   = "/var/lib/efblock" // dossier de travail du service (là où "./data" est résolu)
)

func main() {
	flag.StringVar(&configPath, "config", configPath, "fichier de configuration d'efBlock")
	showVersion := flag.Bool("version", false, "affiche la version et quitte")
	statusOnly := flag.Bool("status", false, "affiche l'état et quitte")
	doUpdate := flag.Bool("update", false, "vérifie et installe la dernière version, sans menu")
	assumeYes := flag.Bool("yes", false, "avec -update : ne demande pas de confirmation")
	applyUpd := flag.Bool("apply-update", false, "exécute la mise à jour demandée depuis l'interface web (lancé par systemd)")
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Println("efblock-console", version)
		return
	case *statusOnly:
		printStatus(os.Stdout)
		return
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "efblock-console doit être lancé en administrateur (root).")
		os.Exit(1)
	}
	if *applyUpd {
		os.Exit(applyUpdate())
	}
	ui := &UI{in: bufio.NewReader(os.Stdin), out: os.Stdout}
	if *doUpdate {
		if err := runUpdate(ui, *assumeYes); err != nil {
			fmt.Fprintln(os.Stderr, "erreur :", err)
			os.Exit(1)
		}
		return
	}
	firstBoot(ui)
	menu(ui)
}

// UI : entrées/sorties du menu.
type UI struct {
	in  *bufio.Reader
	out *os.File
	eof bool // l'entrée s'est fermée pendant une question
}

func (u *UI) printf(format string, a ...any) { fmt.Fprintf(u.out, format, a...) }

// ask pose une question ; une réponse vide donne la valeur par défaut. Retourne false si l'entrée est fermée.
func (u *UI) ask(prompt, def string) (string, bool) {
	if def != "" {
		u.printf("%s [%s] : ", prompt, def)
	} else {
		u.printf("%s : ", prompt)
	}
	line, err := u.in.ReadString('\n')
	if err != nil && line == "" {
		u.eof = true
		return "", false
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, true
	}
	return line, true
}

// confirm pose une question fermée et répète tant que la réponse n'est pas
// « oui » ou « non » (Entrée seule = non). Retourne false si l'entrée est fermée.
func (u *UI) confirm(prompt string) bool {
	for {
		u.printf("%s (y/N) : ", prompt)
		line, err := u.in.ReadString('\n')
		if err != nil && line == "" {
			u.eof = true
			return false
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "", "n", "non", "no":
			return false
		case "o", "oui", "y", "yes":
			return true
		}
		u.printf("Réponds par y (oui) ou n (non).\n")
	}
}

// confirmYes est comme confirm, mais Entrée seule = oui.
func (u *UI) confirmYes(prompt string) bool {
	for {
		u.printf("%s (Y/n) : ", prompt)
		line, err := u.in.ReadString('\n')
		if err != nil && line == "" {
			u.eof = true
			return false
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "", "o", "oui", "y", "yes":
			return true
		case "n", "non", "no":
			return false
		}
		u.printf("Réponds par y (oui) ou n (non).\n")
	}
}

func (u *UI) pause() {
	u.printf("\nAppuie sur Entrée pour revenir au menu… ")
	_, _ = u.in.ReadString('\n')
}

func (u *UI) clear() { u.printf("\033[H\033[2J") }

// --- État ------------------------------------------------------------------

// dataDir : le dossier de données du service ("./data" est relatif à son dossier de travail).
func dataDir(c *config.Config) string {
	if filepath.IsAbs(c.Filter.DataDir) {
		return c.Filter.DataDir
	}
	return filepath.Join(serviceDir, c.Filter.DataDir)
}

// loadSettings charge la configuration (config.yaml + réglages enregistrés).
func loadSettings() (*config.Config, *settings.Manager, error) {
	base, err := config.Load(configPath)
	if err != nil {
		return nil, nil, err
	}
	base.Filter.DataDir = dataDir(base)
	mgr, err := settings.Open(filepath.Join(base.Filter.DataDir, "settings.json"), base)
	if err != nil {
		return nil, nil, err
	}
	return mgr.Effective(), mgr, nil
}

// fixOwnership rend les fichiers de réglages au compte du service : sans ça, efBlock, qui
// tourne sans droits, ne pourrait plus les modifier depuis la page Réglages.
func fixOwnership(paths ...string) {
	u, err := user.Lookup("efblock")
	if err != nil || os.Geteuid() != 0 {
		return
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	for _, p := range paths {
		_ = os.Chown(p, uid, gid)
	}
}

func sh(name string, args ...string) string {
	out, _ := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out))
}

func printStatus(w *os.File) {
	host, _ := os.Hostname()
	ifs := listIfaces()
	ip := firstIPv4(ifs)

	fmt.Fprintf(w, "efBlock %s   (%s)\n", version, host)
	fmt.Fprintln(w, strings.Repeat("-", 60))

	cfg, _, err := loadSettings()
	if err != nil {
		fmt.Fprintf(w, "Configuration illisible : %v\n", err)
	} else {
		scheme := "http"
		if cfg.Web.TLSCert != "" {
			scheme = "https"
		}
		if cfg.Web.PasswordHash == "" {
			fmt.Fprintln(w, "Interface web : DÉSACTIVÉE (aucun mot de passe) : choisis l'option 2")
		} else if ip == "" {
			fmt.Fprintln(w, "Interface web : en attente d'une adresse réseau")
		} else {
			fmt.Fprintf(w, "Interface web : %s://%s:%s\n", scheme, hostOf(cfg.Web.Listen, ip), listenPort(cfg.Web.Listen))
		}
		if ip != "" {
			fmt.Fprintf(w, "Serveur DNS   : %s:%s\n", hostOf(cfg.DNS.Listen, ip), listenPort(cfg.DNS.Listen))
		}
	}
	state := sh("systemctl", "is-active", "efblock")
	if state == "" {
		state = "inconnu"
	}
	fmt.Fprintf(w, "Service       : %s\n", state)

	mode := "DHCP"
	if unit := readFile(networkdFile); unit != "" && !isDHCP(unit) {
		mode = "adresse fixe"
	}
	if len(ifs) == 0 {
		fmt.Fprintln(w, "Réseau        : aucune carte détectée")
	}
	for _, i := range ifs {
		addrs := strings.Join(i.Addrs, ", ")
		if addrs == "" {
			addrs = "pas d'adresse"
		}
		fmt.Fprintf(w, "Réseau        : %s  %s  (%s)\n", i.Name, addrs, mode)
	}
	if gw := parseGateway(readFile("/proc/net/route")); gw != "" {
		fmt.Fprintf(w, "Passerelle    : %s\n", gw)
	}
	fmt.Fprintln(w, strings.Repeat("-", 60))
}

// --- Menu ------------------------------------------------------------------

func menu(ui *UI) {
	for {
		ui.clear()
		printStatus(ui.out)
		ui.printf(`
  1) Réseau (DHCP ou adresse fixe)
  2) Mot de passe de l'interface web
  3) Adresses d'écoute et HTTPS
  4) Redémarrer le service efBlock
  5) Voir les journaux
  6) Mettre à jour efBlock
  7) Shell (administrateur)
  8) Redémarrer la machine
  9) Éteindre la machine

`)
		choice, ok := ui.ask("Choix", "")
		if !ok { // entrée fermée : on quitte (le service systemd relancera le menu)
			return
		}
		switch choice {
		case "1":
			actionNetwork(ui)
		case "2":
			actionPassword(ui)
		case "3":
			actionInfra(ui)
		case "4":
			restartService(ui)
			ui.pause()
		case "5":
			ui.clear()
			ui.printf("%s\n", sh("journalctl", "-u", "efblock", "-n", "40", "--no-pager"))
			ui.pause()
		case "6":
			if err := runUpdate(ui, false); err != nil {
				ui.printf("\nErreur : %v\n", err)
			}
			ui.pause()
		case "7":
			actionShell(ui)
		case "8":
			if ui.confirm("Redémarrer la machine ?") {
				_ = exec.Command("systemctl", "reboot").Run()
				return
			}
		case "9":
			if ui.confirm("Éteindre la machine ?") {
				_ = exec.Command("systemctl", "poweroff").Run()
				return
			}
		}
	}
}

func restartService(ui *UI) {
	ui.printf("\nRedémarrage du service efBlock…\n")
	if out, err := exec.Command("systemctl", "restart", "efblock").CombinedOutput(); err != nil {
		ui.printf("Échec : %v\n%s\n", err, out)
		return
	}
	time.Sleep(2 * time.Second)
	ui.printf("Service : %s\n", sh("systemctl", "is-active", "efblock"))
}

func actionNetwork(ui *UI) {
	ui.clear()
	ui.printf("Réseau\n------\n\n")
	ifs := listIfaces()
	if len(ifs) == 0 {
		ui.printf("Aucune carte réseau détectée.\n")
		ui.pause()
		return
	}
	chosen := -1
	for n, i := range ifs {
		addrs := strings.Join(i.Addrs, ", ")
		if addrs == "" {
			addrs = "pas d'adresse"
		}
		ui.printf("Carte %s   %s\n", i.Name, addrs)
		if len(ifs) == 1 || ui.confirm("Configurer cette carte ?") {
			chosen = n
			break
		}
		ui.printf("\n")
	}
	if chosen < 0 {
		return
	}
	cfg := NetConfig{Iface: ifs[chosen].Name}

	ui.printf("\n")
	if ui.confirmYes("Obtenir l'adresse automatiquement (DHCP) ?") {
		cfg.DHCP = true
	} else {
		def := ""
		if a := ifs[chosen].Addrs; len(a) > 0 {
			def = a[0]
		}
		cfg.Address, _ = ui.ask("Adresse et masque (ex: 192.168.1.10/24)", def)
		def = parseGateway(readFile("/proc/net/route"))
		cfg.Gateway, _ = ui.ask("Passerelle (ex: 192.168.1.1)", def)
	}

	unit, err := cfg.Render()
	if err != nil {
		ui.printf("\nConfiguration refusée : %v\n", err)
		ui.pause()
		return
	}
	ui.printf("\nÀ appliquer :\n%s\n", unit)
	ui.printf("Si tu es connecté en SSH, la connexion va se couper.\n")
	if !ui.confirm("Appliquer cette configuration ?") {
		return
	}
	if err := os.MkdirAll(filepath.Dir(networkdFile), 0o755); err == nil {
		err = os.WriteFile(networkdFile, []byte(unit), 0o644)
	}
	if err != nil {
		ui.printf("Écriture impossible : %v\n", err)
		ui.pause()
		return
	}
	if out, err := exec.Command("systemctl", "restart", "systemd-networkd").CombinedOutput(); err != nil {
		ui.printf("Échec du redémarrage du réseau : %v\n%s\n", err, out)
	} else {
		time.Sleep(3 * time.Second)
		ui.printf("Réseau appliqué.\n")
	}
	ui.pause()
}

func actionPassword(ui *UI) {
	ui.clear()
	ui.printf("Mot de passe de l'interface web\n-------------------------------\n\n8 caractères au moins.\n\n")
	cmd := exec.Command(efblockBin, "-set-password", "-config", configPath)
	cmd.Dir = serviceDir // "./data" doit se résoudre comme pour le service
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		ui.printf("\nMot de passe non modifié.\n")
		ui.pause()
		return
	}
	restartService(ui) // le mot de passe active (ou met à jour) l'interface web
	ui.pause()
}

func actionInfra(ui *UI) {
	cfg, mgr, err := loadSettings()
	if err != nil {
		ui.printf("\nConfiguration illisible : %v\n", err)
		ui.pause()
		return
	}
	ui.clear()
	ui.printf("Adresses d'écoute et HTTPS\n--------------------------\n\n")
	ui.printf("  Écoute DNS           : %s\n", cfg.DNS.Listen)
	ui.printf("  Écoute interface web : %s\n", cfg.Web.Listen)
	https := "non"
	if cfg.Web.TLSCert != "" {
		https = "oui"
	}
	ui.printf("  HTTPS                : %s\n\n", https)
	cur := mgr.InfraOverrides()
	cur.DNSListen, cur.WebListen, cur.TLSCert, cur.TLSKey = cfg.DNS.Listen, cfg.Web.Listen, cfg.Web.TLSCert, cfg.Web.TLSKey
	changed := false

	if ui.confirm("Changer l'écoute DNS ?") {
		cur.DNSListen, _ = ui.ask("Écoute DNS (ex: :53 ou 192.168.1.10:53)", cfg.DNS.Listen)
		changed = true
	}
	if ui.confirm("Changer l'écoute de l'interface web ?") {
		cur.WebListen, _ = ui.ask("Écoute de l'interface web (ex: :8080)", cfg.Web.Listen)
		changed = true
	}
	if cfg.Web.TLSCert == "" {
		if ui.confirm("Activer HTTPS (certificat auto-signé) ?") {
			var ips []net.IP
			for _, i := range listIfaces() {
				for _, a := range i.Addrs {
					if ip, _, err := net.ParseCIDR(a); err == nil {
						ips = append(ips, ip)
					}
				}
			}
			host, _ := os.Hostname()
			cert, key, err := genSelfSigned(certDir, ips, []string{host}, 10*365*24*time.Hour)
			if err != nil {
				ui.printf("\nCertificat impossible : %v\n", err)
				ui.pause()
				return
			}
			cur.TLSCert, cur.TLSKey, changed = cert, key, true
			ui.printf("Certificat créé (valable 10 ans). Ton navigateur affichera un avertissement la première fois : c'est normal, le certificat est le tien.\n")
		}
	} else if ui.confirm("Désactiver HTTPS ?") {
		cur.TLSCert, cur.TLSKey, changed = "", "", true
	}
	if !changed && ui.confirm("Revenir aux valeurs de config.yaml ?") {
		if err := mgr.ClearInfra(); err != nil {
			ui.printf("\nErreur : %v\n", err)
		} else {
			fixOwnership(filepath.Join(dataDir(cfg), "settings.json"))
			ui.printf("\nValeurs de config.yaml rétablies.\n")
			if ui.confirm("Redémarrer le service pour les appliquer ?") {
				restartService(ui)
			}
		}
		ui.pause()
		return
	}
	if changed {
		// SetInfra remplace tout le groupe : on réécrit donc aussi les valeurs inchangées.
		if err := mgr.SetInfra(cur); err != nil {
			ui.printf("\nRefusé : %v\n", err)
			ui.pause()
			return
		}
		fixOwnership(filepath.Join(dataDir(cfg), "settings.json"))
		ui.printf("\nEnregistré. Il faut redémarrer le service pour l'appliquer.\n")
		if ui.confirmYes("Redémarrer le service maintenant ?") {
			restartService(ui)
		}
	}
	ui.pause()
}

// rootHasPassword indique si le compte root a un mot de passe (sinon il est verrouillé).
func rootHasPassword() bool {
	for _, l := range strings.Split(readFile("/etc/shadow"), "\n") {
		f := strings.Split(l, ":")
		if len(f) > 1 && f[0] == "root" {
			return f[1] != "" && f[1] != "!" && f[1] != "*" && !strings.HasPrefix(f[1], "!")
		}
	}
	return false
}

func actionShell(ui *UI) {
	ui.clear()
	if !rootHasPassword() {
		ui.printf("Le compte administrateur n'a pas encore de mot de passe : choisis-en un.\n\n")
		cmd := exec.Command("passwd", "root")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			ui.printf("\nMot de passe non défini.\n")
			ui.pause()
			return
		}
	}
	ui.printf("Connecte-toi avec le compte root. Tape « exit » pour revenir au menu.\n\n")
	// setsid -c : « login » doit être le propriétaire de l'écran (chef de session) pour
	// afficher son invite ; lancé en simple processus fils, il restait muet.
	cmd := exec.Command("setsid", "-c", "login", "-p", "root")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	_ = cmd.Run()
}

// --- Mise à jour -----------------------------------------------------------

func runUpdate(ui *UI, assumeYes bool) error {
	api := os.Getenv("EFBLOCK_UPDATE_API")
	if api == "" {
		api = defaultUpdateAPI
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Minute}

	ui.printf("Recherche de la dernière version…\n")
	rel, err := latestRelease(ctx, client, api, acceptsPrereleases(version))
	if err != nil {
		return fmt.Errorf("impossible de joindre GitHub : %w", err)
	}
	ui.printf("Version installée : %s\nDernière version  : %s\n", version, rel.TagName)
	if !newerThan(version, rel.TagName) {
		ui.printf("\nefBlock est à jour.\n")
		return nil
	}
	name, debURL, sumsURL, err := assetURLs(rel, runtime.GOARCH)
	if err != nil {
		return err
	}
	if !assumeYes && !ui.confirm("\nInstaller "+rel.TagName+" ?") {
		return nil
	}
	ui.printf("Téléchargement de %s…\n", name)
	path, err := downloadVerified(ctx, client, name, debURL, sumsURL, os.TempDir())
	if err != nil {
		return err
	}
	defer os.Remove(path)
	ui.printf("Empreinte vérifiée. Installation…\n")
	// --force-confold : garde ta configuration si tu l'as modifiée. Sans ça, dpkg poserait une
	// question à l'écran et la mise à jour resterait bloquée.
	install := exec.Command("dpkg", "--force-confold", "-i", path)
	install.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	if out, err := install.CombinedOutput(); err != nil {
		return fmt.Errorf("installation : %v\n%s", err, out)
	}
	ui.printf("Mise à jour installée. Redémarrage du service…\n")
	if out, err := exec.Command("systemctl", "restart", "efblock").CombinedOutput(); err != nil {
		return fmt.Errorf("redémarrage : %v\n%s", err, out)
	}
	ui.printf("Terminé : efBlock %s.\n", rel.TagName)
	return nil
}
