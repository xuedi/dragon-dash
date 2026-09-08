// Command dragon-dash serves the dashboard.
//
// Development runs on the desktop against whatever Prometheus is reachable;
// deployment to dragon is the same binary cross-compiled for arm64. Port 80 is
// a deployment concern (a Docker port mapping or a reverse proxy), never
// something this process needs privileges for.
package main

import (
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	"dragon-dash/internal/config"
	"dragon-dash/internal/server"

	// Systems are compiled in and register themselves. Whether they appear is
	// a config decision, not a build one, see internal/system.
	_ "dragon-dash/internal/systems/dragon"
	_ "dragon-dash/internal/systems/fritzhome"
)

func main() {
	addr := flag.String("addr", envOr("DRAGON_DASH_ADDR", "127.0.0.1:8080"),
		"listen address; use :8080 to accept connections from the LAN")
	cfgPath := flag.String("config", envOr("DRAGON_DASH_CONFIG", "config.json"),
		"path to the config file")
	debug := flag.Bool("debug", false, "verbose logging")
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("loading config", "path", *cfgPath, "err", err)
		os.Exit(1)
	}

	srv, err := server.New(cfg, log)
	if err != nil {
		log.Error("starting server", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: a slow Prometheus range query over a year of data
		// can legitimately take a while, and cutting it off mid-response would
		// look like a bug in the chart.
		IdleTimeout: 60 * time.Second,
	}

	log.Info("dragon-dash listening", "addr", *addr, "config", *cfgPath)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
