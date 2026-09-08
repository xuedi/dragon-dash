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
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"dragon-dash/internal/config"
	"dragon-dash/internal/server"
	"dragon-dash/internal/version"

	// Systems are compiled in and register themselves. Whether they appear is
	// a config decision, not a build one, see internal/system.
	_ "dragon-dash/internal/systems/dragon"
	_ "dragon-dash/internal/systems/fritzhome"
)

func main() {
	addr := flag.String("addr", "",
		"listen address, overrides DD_CORE_ADDR; use :8080 to accept connections from the LAN")
	envFiles := flag.String("env", ".env.dist,.env.local",
		"comma-separated env files, later ones win; the real environment wins over all")
	debug := flag.Bool("debug", false, "verbose logging")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	// A deployed binary has to say what it is. Without this the only way to tell
	// which build is running is to checksum it against a local build.
	if *showVersion {
		fmt.Println(version.Version)
		return
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	files := strings.Split(*envFiles, ",")
	cfg, err := config.Load(files...)
	if err != nil {
		log.Error("loading config", "err", err)
		os.Exit(1)
	}

	// The flag wins when given, so a one-off override still works, but a packaged
	// install keeps the address in the same file as every other setting.
	listen := *addr
	if listen == "" {
		listen = cfg.GetOr("core.addr", "127.0.0.1:9494")
	}

	srv, err := server.New(cfg, log, files)
	if err != nil {
		log.Error("starting server", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              listen,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: a slow Prometheus range query over a year of data
		// can legitimately take a while, and cutting it off mid-response would
		// look like a bug in the chart.
		IdleTimeout: 60 * time.Second,
	}

	log.Info("dragon-dash listening", "version", version.Version, "addr", listen, "env", *envFiles)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
