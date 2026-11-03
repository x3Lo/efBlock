// Commande efblock : serveur DNS bloqueur de publicités.
package main

import (
	"context"
	"flag"
	"log/slog"
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
	"github.com/x3Lo/efBlock/internal/upstream"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "chemin du fichier de configuration")
	debug := flag.Bool("debug", false, "logs détaillés (une ligne par requête)")
	flag.Parse()

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
	if n := engine.SetUserRules(cfg.Filter.Allowlist, cfg.Filter.Blocklist); n > 0 {
		slog.Warn("entrées invalides ignorées dans allowlist/blocklist", "nombre", n)
	}
	var sources []filter.Source
	for _, l := range cfg.Filter.Lists {
		sources = append(sources, filter.Source{Name: l.Name, URL: l.URL})
	}
	updater := filter.NewUpdater(engine, sources, cfg.Filter.DataDir, cfg.Filter.UpdateInterval)

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

	if err := srv.Start(); err != nil {
		slog.Error("serveur DNS", "err", err)
		os.Exit(1)
	}
}
