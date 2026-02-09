package gui

import (
	"log"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/jcambass/tailhopper/internal/pac"
	"github.com/jcambass/tailhopper/internal/socks"
	"github.com/jcambass/tailhopper/internal/ts"
)

// ServeDashboard renders the main dashboard page.
func ServeDashboard(w http.ResponseWriter, r *http.Request, tsServer *ts.Server, socksAddr string, connLog *socks.ConnectionLog) {
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
	case ts.StateNeedsMachineAuth:
		showMachineAuthPage(w)
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

	// Build set of known machine names from peers
	knownMachines := make(map[string]bool)
	for _, peer := range status.Peer {
		if len(peer.TailscaleIPs) == 0 {
			continue
		}
		machineName := deriveMachineName(peer.DNSName, peer.HostName, baseDomain)
		knownMachines[machineName] = true
	}

	// Get all connections, then classify at display time
	recent, live := connLog.GetRecent(50)
	allGroups := groupAllConnections(recent, live)
	knownConnections, unknownConnections := classifyConnectionGroups(allGroups, baseDomain, knownMachines)

	// Get our hostname from status
	hostname := ""
	if status.Self != nil {
		hostname = status.Self.HostName
	}

	data := dashboardData{
		BaseDomain:         baseDomain,
		Hostname:           hostname,
		SocksAddr:          socksAddr,
		SocksHost:          socksHost,
		SocksPort:          socksPort,
		PACFileURL:         pac.URLPath,
		Machines:           []machineView{},
		UnknownConnections: unknownConnections,
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

		mv := machineView{
			Name:        machineName,
			DNSName:     peer.DNSName,
			StatusClass: statusClass,
			StatusText:  statusText,
			IPs:         strings.Join(formatIPs(peer.TailscaleIPs), ", "),
		}

		// Merge connection stats if available
		if stats, ok := knownConnections[machineName]; ok {
			mv.ActiveCount = stats.ActiveCount
			mv.ConnectingCount = stats.ConnectingCount
			mv.HasFailed = stats.HasFailed
			mv.BytesSent = stats.BytesSent
			mv.BytesRecv = stats.BytesRecv
		}

		data.Machines = append(data.Machines, mv)
	}

	sort.Slice(data.Machines, func(i, j int) bool {
		// Online machines first, then alphabetically by DNS name
		if data.Machines[i].StatusClass != data.Machines[j].StatusClass {
			return data.Machines[i].StatusClass == "online"
		}
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

func showMachineAuthPage(w http.ResponseWriter) {
	if err := renderTemplate(w, "machine_auth.html", nil); err != nil {
		log.Printf("dashboard: failed to render machine auth template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}
