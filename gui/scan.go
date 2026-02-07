package gui

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jcambass/tailhopper/portscan"
	"github.com/jcambass/tailhopper/ts"
)

// HandleScanPartial returns a handler for scanning a machine and returning the updated machine partial.
func HandleScanPartial(tsServer *ts.Server, scanner *portscan.Scanner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Extract machine name from path: /partials/scan/{name}
		machineName := strings.TrimPrefix(r.URL.Path, "/partials/scan/")
		if machineName == "" {
			http.Error(w, "machine not specified", http.StatusBadRequest)
			return
		}

		baseDomain := tsServer.BaseDomain()

		lc, err := tsServer.LocalClient()
		if err != nil {
			http.Error(w, "failed to get local client", http.StatusInternalServerError)
			return
		}

		status, err := lc.Status(r.Context())
		if err != nil {
			http.Error(w, "failed to get status", http.StatusInternalServerError)
			return
		}

		// Find the peer by machine name
		var foundDNSName string
		var foundOnline bool
		var foundIPs string
		for _, peer := range status.Peer {
			peerName := deriveMachineName(peer.DNSName, peer.HostName, baseDomain)
			if peerName == machineName {
				foundDNSName = peer.DNSName
				foundOnline = peer.Online
				foundIPs = strings.Join(formatIPs(peer.TailscaleIPs), ", ")
				break
			}
		}

		if foundDNSName == "" {
			http.Error(w, "machine not found", http.StatusNotFound)
			return
		}

		statusClass := "offline"
		statusText := "offline"
		if foundOnline {
			statusClass = "online"
			statusText = "online"
		}

		// Start scan in background goroutine with independent context
		// This ensures the scan continues even if the HTTP request is cancelled
		go func() {
			scanCtx, scanCancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer scanCancel()
			scanner.Scan(scanCtx, foundDNSName)
		}()

		// Return immediately with scanning state - htmx polling will pick up results
		mv := machineView{
			Name:         machineName,
			DNSName:      foundDNSName,
			StatusClass:  statusClass,
			StatusText:   statusText,
			IPs:          foundIPs,
			CachedPorts:  nil,
			Scanned:      false,
			DefaultHTTPS: false,
			HasPorts:     false,
			Scanning:     true,
		}

		if err := renderTemplate(w, "machine", mv); err != nil {
			log.Printf("scan partial: failed to render: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	}
}
