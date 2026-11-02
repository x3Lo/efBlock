// Commande efblock : serveur DNS bloqueur de publicités.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/x3Lo/efBlock/internal/config"
	"github.com/x3Lo/efBlock/internal/dnsserver"
	"github.com/x3Lo/efBlock/internal/filter"
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

	fwd := upstream.New(cfg.DNS.Upstreams, cfg.DNS.Timeout)
	srv := dnsserver.New(cfg.DNS.Listen, fwd, engine, cfg.Filter.BlockMode == "nxdomain")

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
