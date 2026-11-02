// Commande efblock : serveur DNS bloqueur de publicités.
package main

import (
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/efBlock/efblock/internal/config"
	"github.com/efBlock/efblock/internal/dnsserver"
	"github.com/efBlock/efblock/internal/upstream"
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

	fwd := upstream.New(cfg.DNS.Upstreams, cfg.DNS.Timeout)
	srv := dnsserver.New(cfg.DNS.Listen, fwd)

	// Arrêt propre sur Ctrl+C ou `systemctl stop` (SIGTERM).
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		slog.Info("arrêt demandé")
		srv.Shutdown()
	}()

	if err := srv.Start(); err != nil {
		slog.Error("serveur DNS", "err", err)
		os.Exit(1)
	}
}
