package ui

import (
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/jcambass/tailhopper/internal/logging"
	"github.com/jcambass/tailhopper/internal/pac"
	"github.com/jcambass/tailhopper/internal/ts"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/key"
)

// ServeDashboard renders the main dashboard page.
func ServeDashboard(w http.ResponseWriter, r *http.Request, tailnet *ts.Tailnet) {
	logger := logging.FromContext(r.Context()).With("component", "dashboard")

	bestEffortSuffix := tailnet.State.BestEffortMagicDNSSuffix()
	if bestEffortSuffix == "" {
		bestEffortSuffix = "Tailnet"
	}

	// Base data structure
	data := dashboardData{
		PACFileURL: pac.URLPath,
		Machines:   []machineView{},
		BaseDomain: bestEffortSuffix,
		State:      tailnet.State.Description(),
	}

	// Handle disabling and disabled state early since it's a state handled fully by us.
	if tailnet.State.Disabling() {
		disabling(w, logger, &data)
		return
	}

	if tailnet.State.Disabled() {
		disabled(w, logger, &data)
		return
	}

	// For all other states, attempt to refresh the state machine and get the latest peer from the tailnet.
	peer, err := tailnet.RefreshState(r.Context())
	if err != nil {
		logger.Printf("dashboard: failed to refresh tailnet state: %v", err)
	}

	// Connected state - show machines and their status
	if ok, suffix := tailnet.State.Connected(); ok {
		socksAddr := tailnet.SocksAddr()
		socksHost, socksPort, _ := net.SplitHostPort(socksAddr)
		data.SocksAddr = socksAddr
		data.SocksHost = socksHost
		data.SocksPort = socksPort
		connected(w, logger, &data, peer, suffix)
		return
	}

	if ok, authURL := tailnet.State.NeedsLogin(); ok {
		needsLogin(w, logger, &data, authURL)
		return
	}

	if ok, suffix := tailnet.State.NeedsMachineAuth(); ok {
		needsMachineAuth(w, logger, &data, suffix)
		return
	}

	if tailnet.State.SlowConnecting() {
		slowConnecting(w, logger, &data)
		return
	}

	if tailnet.State.Connecting() {
		connecting(w, logger, &data)
		return
	}

	// Default case - should not happen, but just in case render 500
	logger.Printf("dashboard: unknown state: %s", tailnet.State.Description())
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func connected(w http.ResponseWriter, logger *logging.Logger, data *dashboardData, peer map[key.NodePublic]*ipnstate.PeerStatus, suffix string) {
	for _, peer := range peer {
		if len(peer.TailscaleIPs) == 0 {
			continue
		}

		machineName := deriveMachineName(peer.DNSName, peer.HostName, suffix)

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

		data.Machines = append(data.Machines, mv)
	}

	sort.Slice(data.Machines, func(i, j int) bool {
		// Online machines first, then alphabetically by DNS name
		if data.Machines[i].StatusClass != data.Machines[j].StatusClass {
			return data.Machines[i].StatusClass == "online"
		}
		return strings.ToLower(data.Machines[i].DNSName) < strings.ToLower(data.Machines[j].DNSName)
	})

	data.BaseDomain = suffix
	data.StateClass = "connected"
	if err := renderTemplate(w, "dashboard.html", data); err != nil {
		logger.Printf("dashboard: failed to render template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}

func disabling(w http.ResponseWriter, logger *logging.Logger, data *dashboardData) {
	data.StateClass = "disabling"
	if err := renderTemplate(w, "dashboard.html", data); err != nil {
		logger.Printf("dashboard: failed to render template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func disabled(w http.ResponseWriter, logger *logging.Logger, data *dashboardData) {
	data.StateClass = "disabled"
	if err := renderTemplate(w, "dashboard.html", data); err != nil {
		logger.Printf("dashboard: failed to render template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func needsLogin(w http.ResponseWriter, logger *logging.Logger, data *dashboardData, authURL string) {
	data.StateClass = "needs-login"
	data.AuthURL = authURL
	if err := renderTemplate(w, "dashboard.html", data); err != nil {
		logger.Printf("dashboard: failed to render template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func needsMachineAuth(w http.ResponseWriter, logger *logging.Logger, data *dashboardData, suffix string) {
	data.StateClass = "needs-auth"
	data.BaseDomain = suffix
	if err := renderTemplate(w, "dashboard.html", data); err != nil {
		logger.Printf("dashboard: failed to render template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func slowConnecting(w http.ResponseWriter, logger *logging.Logger, data *dashboardData) {
	data.StateClass = "connecting-slow"
	if err := renderTemplate(w, "dashboard.html", data); err != nil {
		logger.Printf("dashboard: failed to render template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func connecting(w http.ResponseWriter, logger *logging.Logger, data *dashboardData) {
	data.StateClass = "connecting"
	if err := renderTemplate(w, "dashboard.html", data); err != nil {
		logger.Printf("dashboard: failed to render template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}
