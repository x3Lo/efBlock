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
	"path/filepath"
	"syscall"
	"time"

	"github.com/x3Lo/efBlock/internal/cache"
	"github.com/x3Lo/efBlock/internal/config"
	"github.com/x3Lo/efBlock/internal/dnsserver"
	"github.com/x3Lo/efBlock/internal/filter"
	"github.com/x3Lo/efBlock/internal/querylog"
	"github.com/x3Lo/efBlock/internal/store"
	"github.com/x3Lo/efBlock/internal/upstream"
	"github.com/x3Lo/efBlock/internal/web"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "chemin du fichier de configuration")
	debug := flag.Bool("debug", false, "logs détaillés (une ligne par requête)")
	hashPw := flag.Bool("hash-password", false, "demande un mot de passe et affiche son hash pour config.yaml")
	flag.Parse()

	if *hashPw {
		os.Exit(hashPassword())
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

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

	fwd, err := upstream.New(cfg.DNS.Upstreams, cfg.DNS.Timeout, cfg.DNS.Bootstrap)
	if err != nil {
		slog.Error("upstreams", "err", err)
		os.Exit(1)
	}

	var dnsCache *cache.Cache
	if cfg.Cache.IsEnabled() {
		dnsCache = cache.New(cfg.Cache.MaxEntries, cfg.Cache.MaxTTL)
	}

	// Journal des requêtes (SQLite). Close() à l'arrêt écrit les dernières lignes.
	var qlog *querylog.Logger
	if cfg.Log.IsEnabled() {
		if err := os.MkdirAll(filepath.Dir(cfg.Log.Path), 0o755); err != nil {
			slog.Error("dossier du journal", "err", err)
			os.Exit(1)
		}
		retention := time.Duration(cfg.Log.RetentionDays) * 24 * time.Hour
		if cfg.Log.RetentionDays < 0 {
			retention = 0
		}
		qlog, err = querylog.Open(cfg.Log.Path, retention)
		if err != nil {
			slog.Error("journal des requêtes", "err", err)
			os.Exit(1)
		}
		defer qlog.Close()
	}

	srv := dnsserver.New(dnsserver.Options{
		Log:      qlog,
		Addr:     cfg.DNS.Listen,
		Upstream: fwd,
		Filter:   engine,
		Cache:    dnsCache,
		NXDomain: cfg.Filter.BlockMode == "nxdomain",
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
	if cfg.Web.PasswordHash == "" {
		slog.Warn("interface web désactivée : aucun web.password_hash dans la config (génère-le avec: efblock -hash-password)")
	} else {
		w := web.New(web.Deps{
			Engine: engine, Updater: updater, Store: st, Log: qlog, Cache: dnsCache,
			PasswordHash: cfg.Web.PasswordHash, AllowedNets: cfg.Web.AllowedNets,
			Secure: cfg.Web.TLSCert != "",
		})
		httpSrv = &http.Server{
			Addr:              cfg.Web.Listen,
			Handler:           w.Handler(),
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

// hashPassword lit un mot de passe au clavier (sans l'afficher) et imprime son
// hash bcrypt, à coller dans config.yaml. On ne stocke JAMAIS le mot de passe lui-même.
func hashPassword() int {
	read := func(prompt string) (string, error) {
		fmt.Fprint(os.Stderr, prompt)
		if term.IsTerminal(int(os.Stdin.Fd())) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			return string(b), err
		}
		var line string // entrée redirigée (script) : on lit une ligne
		_, err := fmt.Fscanln(os.Stdin, &line)
		return line, err
	}
	pw, err := read("Mot de passe : ")
	if err != nil || len(pw) < 8 {
		fmt.Fprintln(os.Stderr, "erreur : mot de passe vide ou de moins de 8 caractères")
		return 1
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		if again, _ := read("Confirmation : "); again != pw {
			fmt.Fprintln(os.Stderr, "erreur : les deux saisies diffèrent")
			return 1
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil { // bcrypt refuse les mots de passe de plus de 72 octets
		fmt.Fprintln(os.Stderr, "erreur :", err)
		return 1
	}
	fmt.Println(string(hash))
	return 0
}
