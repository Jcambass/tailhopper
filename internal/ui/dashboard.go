package ui

import (
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/jcambass/tailhopper/internal/logging"
	"github.com/jcambass/tailhopper/internal/pac"
	"github.com/jcambass/tailhopper/internal/ts"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
)

// ServeDashboard renders the main dashboard page.
func ServeDashboard(w http.ResponseWriter, r *http.Request, tailnet *ts.Tailnet) {
	logger := logging.FromContext(r.Context()).With("component", "dashboard")

	state := tailnet.LatestState()

	bestEffortDomain := "Tailnet"
	if state.MagicDNSSuffix != "" {
		bestEffortDomain = state.MagicDNSSuffix
	}

	// Base data structure
	data := dashboardData{
		PACFileURL: pac.URLPath,
		Machines:   []machineView{},
		BaseDomain: bestEffortDomain,
	}

	if state.State == nil {
		disabled(w, logger, &data)
		return
	}

	switch *state.State {
	case ipn.NoState:
		disabled(w, logger, &data)
		return
	case ipn.InUseOtherUser:
		// should never happen to us. Consider failure for now.
		panic("unexpected state: InUseOtherUser")
	case ipn.NeedsLogin:
		// TODO: Guard against nil BrowseToURL.
		needsLogin(w, logger, &data, *state.BrowseToURL)
		return
	case ipn.NeedsMachineAuth:
		needsMachineAuth(w, logger, &data, bestEffortDomain)
		return
	case ipn.Stopped:
		disabled(w, logger, &data)
		return
	case ipn.Starting:
		connecting(w, logger, &data)
		return
	case ipn.Running:
		socksAddr, ready := tailnet.SocksAddr()
		if !ready {
			logger.Printf("SOCKS5 proxy is not ready yet")
			data.SocksAddr = "N/A"
			data.SocksHost = "N/A"
			data.SocksPort = "N/A"
			// We can still render the dashboard without the SOCKS address, so we continue instead of returning an error.
		} else {
			socksHost, socksPort, _ := net.SplitHostPort(socksAddr)
			data.SocksAddr = socksAddr
			data.SocksHost = socksHost
			data.SocksPort = socksPort
		}

		connected(w, logger, &data, state.Peers, bestEffortDomain)
		return
	}

	// Default case - should not happen, but just in case render 500
	logger.Printf("dashboard: unknown state: %s", state.String())
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func connected(w http.ResponseWriter, logger *logging.Logger, data *dashboardData, peer []tailcfg.NodeView, suffix string) {
	for _, peer := range peer {
		if peer.Addresses().Len() == 0 {
			continue
		}

		machineName := peer.ComputedName()

		statusClass := "offline"
		statusText := "offline"
		if peer.Online().Get() {
			statusClass = "online"
			statusText = "online"
		}

		mv := machineView{
			Name:        machineName,
			DNSName:     peer.Name(),
			StatusClass: statusClass,
			StatusText:  statusText,
			IPs:         strings.Join(formatIPs(peer.Addresses().AsSlice()), ", "),
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
