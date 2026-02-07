package gui

import (
	"log"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/jcambass/tailhopper/pac"
	"github.com/jcambass/tailhopper/portscan"
	"github.com/jcambass/tailhopper/socks"
	"github.com/jcambass/tailhopper/ts"
)

// ServeDashboard renders the main dashboard page.
func ServeDashboard(w http.ResponseWriter, r *http.Request, tsServer *ts.Server, socksAddr string, connLog *socks.ConnectionLog, scanner *portscan.Scanner) {
	baseDomain := tsServer.BaseDomain()
	state := tsServer.State().Current()

	// Handle non-running states
	switch state.State {
	case ts.StateError:
		log.Printf("dashboard: tsnet error: %v", state.Error)
		showErrorPage(w, state.Error.Error())
		return
	case ts.StateNeedsLogin:
		showLoginPage(w, state.AuthURL)
		return
	case ts.StateConnectingSlow:
		showSlowConnectionPage(w, baseDomain)
		return
	case ts.StateConnecting:
		showLoadingPage(w, baseDomain)
		return
	}

	// StateRunning - show dashboard
	lc, err := tsServer.LocalClient()
	if err != nil {
		log.Printf("dashboard: failed to get local client: %v", err)
		showLoadingPage(w, baseDomain)
		return
	}

	ctx := r.Context()
	status, err := lc.Status(ctx)
	if err != nil {
		log.Printf("dashboard: failed to get status: %v", err)
		showLoadingPage(w, baseDomain)
		return
	}

	// Parse host and port from socksAddr
	socksHost, socksPort, _ := net.SplitHostPort(socksAddr)

	// Get connections
	recent, live := connLog.GetRecent(20)
	connections := make([]connectionView, 0, len(live)+len(recent))
	for _, c := range live {
		connections = append(connections, connectionView{
			Host:      c.Host,
			Port:      c.Port,
			StartTime: c.StartTime,
			BytesSent: c.BytesSent,
			BytesRecv: c.BytesRecv,
			Active:    true,
		})
	}
	for _, c := range recent {
		connections = append(connections, connectionView{
			Host:      c.Host,
			Port:      c.Port,
			StartTime: c.StartTime,
			EndTime:   c.EndTime,
			BytesSent: c.BytesSent,
			BytesRecv: c.BytesRecv,
			Error:     c.Error,
		})
	}

	// Get our hostname from status
	hostname := ""
	if status.Self != nil {
		hostname = status.Self.HostName
	}

	data := dashboardData{
		BaseDomain:  baseDomain,
		Hostname:    hostname,
		SocksAddr:   socksAddr,
		SocksHost:   socksHost,
		SocksPort:   socksPort,
		PACFileURL:  pac.URLPath,
		Machines:    []machineView{},
		Connections: connections,
	}

	for _, peer := range status.Peer {
		if len(peer.TailscaleIPs) == 0 {
			continue
		}

		machineName := deriveMachineName(peer.DNSName, peer.HostName, baseDomain)

		statusClass := "offline"
		statusText := "offline"
		if peer.Online {
			statusClass = "online"
			statusText = "online"
		}

		cachedPorts, scanned := scanner.GetCachedResults(peer.DNSName)
		scanning := scanner.IsScanning(peer.DNSName)

		hasPorts := len(cachedPorts) > 0
		defaultHTTPS := false
		if hasPorts {
			if cachedPorts[0] == 443 || cachedPorts[0] == 8448 {
				defaultHTTPS = true
			}
		}

		data.Machines = append(data.Machines, machineView{
			Name:         machineName,
			DNSName:      peer.DNSName,
			StatusClass:  statusClass,
			StatusText:   statusText,
			IPs:          strings.Join(formatIPs(peer.TailscaleIPs), ", "),
			CachedPorts:  cachedPorts,
			Scanned:      scanned,
			DefaultHTTPS: defaultHTTPS,
			HasPorts:     hasPorts,
			Scanning:     scanning,
		})
	}

	sort.Slice(data.Machines, func(i, j int) bool {
		return strings.ToLower(data.Machines[i].DNSName) < strings.ToLower(data.Machines[j].DNSName)
	})

	if err := renderTemplate(w, "dashboard.html", data); err != nil {
		log.Printf("dashboard: failed to render template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}

func showLoadingPage(w http.ResponseWriter, baseDomain string) {
	if baseDomain == "" {
		baseDomain = "Tailscale"
	}
	data := struct {
		BaseDomain string
	}{
		BaseDomain: baseDomain,
	}

	if err := renderTemplate(w, "loading.html", data); err != nil {
		log.Printf("dashboard: failed to render loading template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}

func showErrorPage(w http.ResponseWriter, errMsg string) {
	data := struct {
		Error string
	}{
		Error: errMsg,
	}

	if err := renderTemplate(w, "error.html", data); err != nil {
		log.Printf("dashboard: failed to render error template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}

func showSlowConnectionPage(w http.ResponseWriter, baseDomain string) {
	if baseDomain == "" {
		baseDomain = "Tailscale"
	}
	data := struct {
		BaseDomain string
	}{
		BaseDomain: baseDomain,
	}

	if err := renderTemplate(w, "slow.html", data); err != nil {
		log.Printf("dashboard: failed to render slow template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}

func showLoginPage(w http.ResponseWriter, authURL string) {
	data := struct {
		AuthURL string
	}{
		AuthURL: authURL,
	}

	if err := renderTemplate(w, "login.html", data); err != nil {
		log.Printf("dashboard: failed to render login template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}
