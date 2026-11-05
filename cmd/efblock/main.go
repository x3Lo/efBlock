// Commande efblock : serveur DNS bloqueur de publicités.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/x3Lo/efBlock/internal/cache"
	"github.com/x3Lo/efBlock/internal/config"
	"github.com/x3Lo/efBlock/internal/dnsserver"
	"github.com/x3Lo/efBlock/internal/filter"
	"github.com/x3Lo/efBlock/internal/localdns"
	"github.com/x3Lo/efBlock/internal/querylog"
	"github.com/x3Lo/efBlock/internal/settings"
	"github.com/x3Lo/efBlock/internal/store"
	"github.com/x3Lo/efBlock/internal/upstream"
	"github.com/x3Lo/efBlock/internal/web"
)

// version est renseignée à la compilation (-ldflags "-X main.version=1.2.3").
var version = "dev"

func main() {
	cfgPath := flag.String("config", "config.yaml", "chemin du fichier de configuration")
	debug := flag.Bool("debug", false, "logs détaillés (une ligne par requête)")
	hashPw := flag.Bool("hash-password", false, "demande un mot de passe et affiche son hash pour config.yaml")
	setPw := flag.Bool("set-password", false, "demande un mot de passe et l'enregistre comme mot de passe de l'interface web")
	showVersion := flag.Bool("version", false, "affiche la version et quitte")
	flag.Parse()

	if *showVersion {
		fmt.Println("efblock", version)
		return
	}
	if *hashPw {
		os.Exit(hashPassword())
	}
	if *setPw {
		os.Exit(setWebPassword(*cfgPath))
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	base, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	// Réglages modifiables depuis l'interface web (settings.json) : ils remplacent
	// ceux de config.yaml pour les groupes modifiés. cfg est la configuration EN VIGUEUR.
	mgr, err := settings.Open(filepath.Join(base.Filter.DataDir, "settings.json"), base)
	if err != nil {
		slog.Error("réglages", "err", err)
		os.Exit(1)
	}
	cfg := mgr.Effective()

	// Moteur de filtrage : allowlist/blocklist perso + listes téléchargées.
	engine := filter.NewEngine()
	var sources []filter.Source
	for _, l := range cfg.Filter.Lists {
		sources = append(sources, filter.Source{Name: l.Name, URL: l.URL})
	}
	updater := filter.NewUpdater(engine, sources, cfg.Filter.DataDir, cfg.Filter.UpdateInterval)

	// Le Store fusionne config.yaml et les réglages faits depuis l'interface web
	// (state.json), puis les applique au moteur.
	st, err := store.Open(filepath.Join(cfg.Filter.DataDir, "state.json"),
		cfg.Filter.Allowlist, cfg.Filter.Blocklist, sources, engine, updater)
	if err != nil {
		slog.Error("état", "err", err)
		os.Exit(1)
	}

	fwd, zone, err := buildDNS(cfg)
	if err != nil {
		slog.Error("configuration DNS", "err", err)
		os.Exit(1)
	}

	// Le cache peut être remplacé à chaud (taille, durée) : on garde un pointeur partagé.
	var liveCache atomic.Pointer[cache.Cache]
	liveCache.Store(newCache(cfg))
	engine.SetCNAMECheck(cfg.Filter.CNAMEEnabled())

	// Journal des requêtes (SQLite). Close() à l'arrêt écrit les dernières lignes.
	var qlog *querylog.Logger
	if cfg.Log.IsEnabled() {
		if err := os.MkdirAll(filepath.Dir(cfg.Log.Path), 0o755); err != nil {
			slog.Error("dossier du journal", "err", err)
			os.Exit(1)
		}
		qlog, err = querylog.Open(cfg.Log.Path, retention(cfg))
		if err != nil {
			slog.Error("journal des requêtes", "err", err)
			os.Exit(1)
		}
		defer qlog.Close()
	}

	srv := dnsserver.New(dnsserver.Options{
		Log:            qlog,
		Addr:           cfg.DNS.Listen,
		Upstream:       fwd,
		Filter:         engine,
		Cache:          liveCache.Load(),
		NXDomain:       cfg.Filter.BlockMode == "nxdomain",
		AllowedClients: cfg.DNS.AllowedNets,
		Local:          zone,
	})

	// ctx est annulé à l'arrêt : il stoppe proprement la mise à jour des listes.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Les listes se chargent en arrière-plan : le DNS répond dès le démarrage.
	go updater.Run(ctx)

	// Arrêt propre sur Ctrl+C ou `systemctl stop` (SIGTERM).
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		slog.Info("arrêt demandé")
		cancel()
		srv.Shutdown()
	}()

	// Interface web (seulement si un mot de passe est configuré).
	var httpSrv *http.Server
	var webSrv *web.Server
	if cfg.Web.PasswordHash == "" {
		slog.Warn("interface web désactivée : aucun web.password_hash dans la config (génère-le avec: efblock -hash-password)")
	} else {
		// Sous systemd, "redémarrer" = quitter proprement : Restart=always relance le service.
		var restart func()
		if os.Getenv("INVOCATION_ID") != "" {
			restart = func() { _ = syscall.Kill(os.Getpid(), syscall.SIGTERM) }
		}
		webSrv = web.New(web.Deps{
			Engine: engine, Updater: updater, Store: st, Log: qlog,
			Cache:        liveCache.Load,
			PasswordHash: cfg.Web.PasswordHash, AllowedNets: cfg.Web.AllowedNets,
			Secure:   cfg.Web.TLSCert != "",
			Settings: mgr,
			Upstream: srv.Upstream,
			ClientName: func(ip string) string {
				return srv.Local().ClientName(ip)
			},
			Restart: restart,
		})
		httpSrv = &http.Server{
			Addr:              cfg.Web.Listen,
			Handler:           webSrv.Handler(),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       2 * time.Minute,
		}
		go func() {
			slog.Info("interface web démarrée", "addr", cfg.Web.Listen, "https", cfg.Web.TLSCert != "")
			var err error
			if cfg.Web.TLSCert != "" {
				err = httpSrv.ListenAndServeTLS(cfg.Web.TLSCert, cfg.Web.TLSKey)
			} else {
				err = httpSrv.ListenAndServe()
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("interface web", "err", err)
			}
		}()
	}

	// Mise en application à chaud d'une configuration modifiée depuis l'interface.
	// Tout est construit AVANT de remplacer quoi que ce soit : si une valeur est
	// refusée (upstream mal écrit, hôte invalide...), rien ne change.
	mgr.SetApply(func(c *config.Config) error {
		newFwd, newZone, err := buildDNS(c)
		if err != nil {
			return err
		}
		srv.SetUpstream(newFwd)
		srv.SetLocal(newZone)
		srv.SetAllowedClients(c.DNS.AllowedNets)
		srv.SetNXDomain(c.Filter.BlockMode == "nxdomain")
		nc := newCache(c) // nil si le cache est désactivé
		liveCache.Store(nc)
		srv.SetCache(nc)
		engine.SetCNAMECheck(c.Filter.CNAMEEnabled())
		updater.SetInterval(c.Filter.UpdateInterval)
		qlog.SetRetention(retention(c))
		if webSrv != nil {
			webSrv.SetAllowedNets(c.Web.AllowedNets)
			webSrv.SetPasswordHash(c.Web.PasswordHash)
		}
		slog.Info("configuration appliquée à chaud")
		return nil
	})

	// Arrêt de l'interface web en même temps que le DNS.
	stopWeb := func() {
		if httpSrv != nil {
			c, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			_ = httpSrv.Shutdown(c)
		}
	}
	defer stopWeb()

	if err := srv.Start(); err != nil {
		slog.Error("serveur DNS", "err", err)
		os.Exit(1)
	}
}

// buildDNS construit les résolveurs amont et la zone locale d'une configuration.
func buildDNS(c *config.Config) (*upstream.Forwarder, *localdns.Zone, error) {
	fwd, err := upstream.New(c.DNS.Upstreams, c.DNS.Timeout, c.DNS.Bootstrap)
	if err != nil {
		return nil, nil, err
	}
	zone, err := localdns.New(c.DNS.Local.Upstream, c.DNS.Local.Domains, c.DNS.Local.Hosts, c.DNS.Timeout)
	if err != nil {
		return nil, nil, err
	}
	return fwd, zone, nil
}

// newCache construit le cache d'une configuration (nil s'il est désactivé).
func newCache(c *config.Config) *cache.Cache {
	if !c.Cache.IsEnabled() {
		return nil
	}
	dc := cache.New(c.Cache.MaxEntries, c.Cache.MaxTTL)
	dc.SetStale(c.Cache.StaleWindow())
	dc.SetPrefetch(c.Cache.PrefetchEnabled())
	return dc
}

// retention : durée de conservation du journal (0 = illimitée).
func retention(c *config.Config) time.Duration {
	if c.Log.RetentionDays < 0 {
		return 0
	}
	return time.Duration(c.Log.RetentionDays) * 24 * time.Hour
}

// readNewPassword demande un mot de passe (sans l'afficher) et retourne son hash bcrypt.
// Sur un terminal, il demande une confirmation ; si l'entrée est redirigée (script), il lit une ligne.
func readNewPassword() (string, error) {
	read := func(prompt string) (string, error) {
		fmt.Fprint(os.Stderr, prompt)
		if term.IsTerminal(int(os.Stdin.Fd())) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			return string(b), err
		}
		var line string
		_, err := fmt.Fscanln(os.Stdin, &line)
		return line, err
	}
	pw, err := read("Mot de passe : ")
	if err != nil || len(pw) < 8 {
		return "", errors.New("mot de passe vide ou de moins de 8 caractères")
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		if again, _ := read("Confirmation : "); again != pw {
			return "", errors.New("les deux saisies diffèrent")
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil { // bcrypt refuse les mots de passe de plus de 72 octets
		return "", err
	}
	return string(hash), nil
}

// hashPassword affiche le hash bcrypt d'un mot de passe, à coller dans config.yaml.
// On ne stocke JAMAIS le mot de passe lui-même.
func hashPassword() int {
	hash, err := readNewPassword()
	if err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		return 1
	}
	fmt.Println(hash)
	return 0
}

// setWebPassword enregistre le mot de passe de l'interface web dans settings.json
// (le même fichier que la page Réglages) : pratique pour le premier démarrage d'un
// système tout installé, où personne n'a encore de mot de passe.
func setWebPassword(cfgPath string) int {
	base, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "erreur : config :", err)
		return 1
	}
	hash, err := readNewPassword()
	if err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		return 1
	}
	path := filepath.Join(base.Filter.DataDir, "settings.json")
	if err := os.MkdirAll(base.Filter.DataDir, 0o750); err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		return 1
	}
	mgr, err := settings.Open(path, base)
	if err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		return 1
	}
	if err := mgr.SetPasswordHash(hash); err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		return 1
	}
	// Lancé en root, on rend le fichier au compte du service : sinon efBlock, qui tourne
	// sans droits, ne pourrait plus le modifier depuis la page Réglages.
	if u, err := user.Lookup("efblock"); err == nil && os.Geteuid() == 0 {
		uid, _ := strconv.Atoi(u.Uid)
		gid, _ := strconv.Atoi(u.Gid)
		_ = os.Chown(path, uid, gid)
		_ = os.Chown(base.Filter.DataDir, uid, gid)
	}
	fmt.Fprintln(os.Stderr, "Mot de passe de l'interface web enregistré. Redémarre le service pour l'activer : systemctl restart efblock")
	return 0
}
