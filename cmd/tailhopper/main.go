package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/jcambass/tailhopper/internal/logging"
	"github.com/jcambass/tailhopper/internal/registry"
	"github.com/jcambass/tailhopper/internal/sse"
	"github.com/jcambass/tailhopper/internal/web"
)

// version is injected at build time via -ldflags.
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	// Set up context-aware logging
	var level slog.Level
	if err := level.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level:       level,
		AddSource:   true,
		ReplaceAttr: logging.SimplifySource(),
	}

	// Use text handler for logfmt structured logging, pipe through hl for colored output
	handler := logging.NewContextHandler(slog.NewTextHandler(os.Stderr, opts))
	logger := slog.New(handler)
	slog.SetDefault(logger)

	ctx := context.Background()
	defer logging.CatchPanic(ctx)

	seeBroadcaster := sse.NewSSEBroadcaster()

	reg, err := registry.NewRegistry("./tailhopper.json", seeBroadcaster)
	if err != nil {
		slog.ErrorContext(ctx, "failed to initialize registry", slog.Any("error", err))
		os.Exit(1)
	}

	// Restore previously user-enabled tailnets so UI intent and runtime state stay aligned after restart.
	reg.RestoreEnabledTailnets(ctx)

	// Dashboard server on separate port
	dashboardPort := os.Getenv("HTTP_PORT")
	if dashboardPort == "" {
		dashboardPort = "8888"
	}
	dashboardAddr := "127.0.0.1:" + dashboardPort

	// Create dashboard server
	dashboardSrv := web.NewServer(dashboardAddr, reg, seeBroadcaster)

	if err := dashboardSrv.Start(); err != nil {
		slog.ErrorContext(ctx, "dashboard server error", slog.Any("error", err))
		os.Exit(1)
	}
}
