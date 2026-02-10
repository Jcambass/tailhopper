package main

import (
	"context"
	"log"
	"os"

	"github.com/jcambass/tailhopper/internal/logging"
	"github.com/jcambass/tailhopper/internal/socks"
	"github.com/jcambass/tailhopper/internal/ts"
	"github.com/jcambass/tailhopper/internal/web"
	"tailscale.com/util/dnsname"
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

	baseLogger := logging.New(log.Default(), map[string]string{})
	logging.SetDefault(baseLogger)
	logger := baseLogger.With("component", "cmd")
	ctx := context.Background()

	hostname := os.Getenv("TS_HOSTNAME")
	if hostname == "" {
		if realHostname, err := os.Hostname(); err == nil {
			// Use Tailscale's hostname sanitization logic
			hostname = dnsname.SanitizeHostname(realHostname) + "-tailhopper"
		} else {
			hostname = "tailhopper"
		}
	}

	// Create Tailnet
	tailnet := ts.NewTailnet("./tsnet-state", hostname, nil)
	defer tailnet.Stop(ctx)

	// SOCKS5 proxy server configuration
	socksPort := os.Getenv("SOCKS_PORT")
	if socksPort == "" {
		socksPort = "1080"
	}
	socksAddr := "127.0.0.1:" + socksPort

	// Start SOCKS5 proxy
	socksSrv, err := socks.NewServer(socksAddr, tailnet.Dial)
	if err != nil {
		logger.Fatalf("failed to start SOCKS5 proxy: %v", err)
	}
	defer socksSrv.Close()
	socksSrv.Start()

	// Dashboard server on separate port
	dashboardPort := os.Getenv("HTTP_PORT")
	if dashboardPort == "" {
		dashboardPort = "8888"
	}
	dashboardAddr := "127.0.0.1:" + dashboardPort

	// Create dashboard server
	dashboardSrv := web.NewServer(dashboardAddr, tailnet, socksAddr)

	if err := dashboardSrv.Start(); err != nil {
		logger.Fatalf("dashboard server error: %v", err)
	}
}
