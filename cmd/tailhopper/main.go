package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/jcambass/tailhopper/internal/logging"
	"github.com/jcambass/tailhopper/internal/sse"
	"github.com/jcambass/tailhopper/internal/ts"
	"github.com/jcambass/tailhopper/internal/web"
)

// Tailhopper: A SOCKS5 proxy for personal Tailnet users.
// This program provides SOCKS5 proxying through your Tailnet.
// Example usage:
//  1. Optionally set TS_HOSTNAME to customize the Tailscale hostname (defaults to "{hostname}-tailhopper").
//  2. Optionally set HTTP_PORT to change the dashboard port (defaults to 8888).
//  3. Optionally set SOCKS_PORT to change the SOCKS5 proxy port (defaults to 1080).
//  4. Run this program.
//  5. On first start, view stdout for a URL to authenticate with your Tailnet.
//  6. Configure your browser to use the PAC file at http://localhost:8888/proxy.pac for automatic configuration
//     or configure SOCKS5 proxy to use localhost:1080.
//  7. View status at http://localhost:8888
func main() {
	// Set up context-aware logging
	var level slog.Level
	if err := level.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	// We use NewTextHandler to avoid a deadlock loop with the default handler
	// which would otherwise route back to the log package when SetDefault is called.
	handler := logging.NewContextHandler(slog.NewTextHandler(os.Stderr, opts))
	logger := slog.New(handler)
	slog.SetDefault(logger)

	ctx := context.Background()
	defer logging.CatchPanic(ctx)

	seeBroadcaster := sse.NewSSEBroadcaster()

	registry, err := ts.NewRegistry("./tailhopper.json", seeBroadcaster)
	if err != nil {
		slog.ErrorContext(ctx, "failed to initialize registry", "error", err)
		os.Exit(1)
	}

	// Dashboard server on separate port
	dashboardPort := os.Getenv("HTTP_PORT")
	if dashboardPort == "" {
		dashboardPort = "8888"
	}
	dashboardAddr := "127.0.0.1:" + dashboardPort

	// Create dashboard server
	dashboardSrv := web.NewServer(dashboardAddr, registry, seeBroadcaster)

	if err := dashboardSrv.Start(); err != nil {
		slog.ErrorContext(ctx, "dashboard server error", "error", err)
		os.Exit(1)
	}
}
