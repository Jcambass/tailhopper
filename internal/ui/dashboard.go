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
func ServeDashboard(w http.ResponseWriter, r *http.Request, tailnet *ts.Tailnet, socksAddr string) {
	state := tailnet.State.Current()

	// Parse host and port from socksAddr
	socksHost, socksPort, _ := net.SplitHostPort(socksAddr)

	// Base data structure
	data := dashboardData{
		BaseDomain: tailnet.State.MagicDNSSuffix(),
		SocksAddr:  socksAddr,
		SocksHost:  socksHost,
		SocksPort:  socksPort,
		PACFileURL: pac.URLPath,
		Machines:   []machineView{},
		State:      state.String(),
	}

	// Try to get status, might change the state
	status, err := tailnet.Status(r.Context())
	if err != nil {
		log.Printf("dashboard: failed to get status: %v", err)
		// Status is updated internally by the tailnet state machine, so we can just continue and render the appropriate state card
	}

	// Handle non-connected states - show dashboard with state card
	switch state {
	case ts.StateConnected:
		for _, peer := range status.Peer {
			if len(peer.TailscaleIPs) == 0 {
				continue
			}

			machineName := deriveMachineName(peer.DNSName, peer.HostName, tailnet.State.MagicDNSSuffix())

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

		data.StateClass = "connected"
		if err := renderTemplate(w, "dashboard.html", data); err != nil {
			log.Printf("dashboard: failed to render template: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	case ts.StateFailed:
		err := tailnet.State.Error()
		log.Printf("dashboard: tsnet error: %v", err)
		data.StateClass = "error"
		if err != nil {
			data.ErrorMsg = err.Error()
		}
		if err := renderTemplate(w, "dashboard.html", data); err != nil {
			log.Printf("dashboard: failed to render template: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	case ts.StateDisabled:
		data.StateClass = "disabled"
		if err := renderTemplate(w, "dashboard.html", data); err != nil {
			log.Printf("dashboard: failed to render template: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	case ts.StateNeedsLogin:
		data.StateClass = "needs-login"
		data.AuthURL = tailnet.State.AuthURL()
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
}
