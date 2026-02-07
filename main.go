package main

import (
	"log"
	"os"

	"github.com/jcambass/tailhopper/socks"
	"github.com/jcambass/tailhopper/ts"
	"github.com/jcambass/tailhopper/web"
	"tailscale.com/util/dnsname"
)

// Tailhopper: A SOCKS5 proxy for personal Tailnet users.
// This program uses tsnet's built-in SOCKS5 proxy to route connections through your Tailnet.
// Example usage:
//  1. Optionally set TS_HOSTNAME to customize the Tailscale hostname (defaults to "{hostname}-tailhopper").
//  2. Optionally set HTTP_PORT to change the HTTP/GUI port (defaults to 8888).
//  3. Optionally set SOCKS_PORT to change the SOCKS5 proxy port (defaults to 1080).
//  4. Run this program.
//  5. On first start, view stdout for a URL to authenticate with your Tailnet.
//  6. Configure your browser to use the PAC file at http://localhost:8888/proxy.pac
//     Or manually set SOCKS5 proxy to the address shown on startup.
func main() {
	hostname := os.Getenv("TS_HOSTNAME")
	if hostname == "" {
		if realHostname, err := os.Hostname(); err == nil {
			// Use Tailscale's hostname sanitization logic
			hostname = dnsname.SanitizeHostname(realHostname) + "-tailhopper"
		} else {
			hostname = "tailhopper"
		}
	}

	// Create Tailscale server
	tsServer := ts.NewServer("./tsnet-state", hostname)
	defer tsServer.Close()

	// Start SOCKS5 proxy
	socksPort := os.Getenv("SOCKS_PORT")
	if socksPort == "" {
		socksPort = "1080"
	}
	socksAddr := "127.0.0.1:" + socksPort

	socksServer, err := socks.NewServer(socksAddr, tsServer.Dial)
	if err != nil {
		log.Fatalf("failed to create SOCKS5 server: %v", err)
	}
	defer socksServer.Close()

	socksServer.Start()

	// HTTP server configuration
	httpPort := os.Getenv("HTTP_PORT")
	if httpPort == "" {
		httpPort = "8888"
	}
	httpAddr := "127.0.0.1:" + httpPort

	// Create HTTP server
	httpSrv := web.NewServer(httpAddr, tsServer, socksAddr, socksServer.ConnLog)

	// Start tsnet connection
	tsServer.Start()

	if err := httpSrv.Start(); err != nil {
		log.Fatal(err)
	}
}
