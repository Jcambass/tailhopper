package main

import (
	"log"
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
	defer logging.CatchPanic(nil)

	baseLogger := logging.New(log.Default(), map[string]any{})
	logging.SetDefault(baseLogger)
	logger := baseLogger.With("component", "cmd")

	seeBroadcaster := sse.NewSSEBroadcaster(logger)

	registry, err := ts.NewRegistry("./tailhopper.json", baseLogger, seeBroadcaster)
	if err != nil {
		logger.Fatalf("failed to initialize registry: %v", err)
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
		logger.Fatalf("dashboard server error: %v", err)
	}
}
