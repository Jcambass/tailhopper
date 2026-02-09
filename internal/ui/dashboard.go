package ui

import (
	"log"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/jcambass/tailhopper/internal/pac"
	"github.com/jcambass/tailhopper/internal/ts"
)

// ServeDashboard renders the main dashboard page.
func ServeDashboard(w http.ResponseWriter, r *http.Request, tsServer *ts.Server, socksAddr string) {
	baseDomain := tsServer.BaseDomain()
	state := tsServer.State().Current()

	// Parse host and port from socksAddr
	socksHost, socksPort, _ := net.SplitHostPort(socksAddr)

	// Base data structure
	data := dashboardData{
		BaseDomain: baseDomain,
		SocksAddr:  socksAddr,
		SocksHost:  socksHost,
		SocksPort:  socksPort,
		PACFileURL: pac.URLPath,
		Machines:   []machineView{},
		State:      state.State.String(),
		IsHtmx:     r.Header.Get("HX-Request") != "",
	}

	// Handle non-running states - show dashboard with state card
	switch state.State {
	case ts.StateError:
		log.Printf("dashboard: tsnet error: %v", state.Error)
		data.StateClass = "error"
		if state.Error != nil {
			data.ErrorMsg = state.Error.Error()
		}
		if err := renderTemplate(w, "dashboard.html", data); err != nil {
			log.Printf("dashboard: failed to render template: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	case ts.StateNeedsLogin:
		data.StateClass = "needs-login"
		data.AuthURL = state.AuthURL
		if err := renderTemplate(w, "dashboard.html", data); err != nil {
			log.Printf("dashboard: failed to render template: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	case ts.StateNeedsMachineAuth:
		data.StateClass = "needs-auth"
		if err := renderTemplate(w, "dashboard.html", data); err != nil {
			log.Printf("dashboard: failed to render template: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	case ts.StateConnectingSlow:
		data.StateClass = "connecting-slow"
		if err := renderTemplate(w, "dashboard.html", data); err != nil {
			log.Printf("dashboard: failed to render template: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	case ts.StateConnecting:
		data.StateClass = "connecting"
		if err := renderTemplate(w, "dashboard.html", data); err != nil {
			log.Printf("dashboard: failed to render template: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}

	// StateRunning - fetch machines
	data.StateClass = "running"

	lc, err := tsServer.LocalClient()
	if err != nil {
		log.Printf("dashboard: failed to get local client: %v", err)
		data.StateClass = "connecting"
		if err := renderTemplate(w, "dashboard.html", data); err != nil {
			log.Printf("dashboard: failed to render template: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}

	ctx := r.Context()
	status, err := lc.Status(ctx)
	if err != nil {
		log.Printf("dashboard: failed to get status: %v", err)
		data.StateClass = "connecting"
		if err := renderTemplate(w, "dashboard.html", data); err != nil {
			log.Printf("dashboard: failed to render template: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}

	// Get our hostname from status
	if status.Self != nil {
		data.Hostname = status.Self.HostName
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
