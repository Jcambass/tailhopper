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
)

// ServeDashboard renders the main dashboard page.
func ServeDashboard(w http.ResponseWriter, r *http.Request, registry *ts.Registry) {
	logger := logging.FromContext(r.Context()).With("component", "dashboard")

	// Base data structure
	data := dashboardData{
		PACFileURL:              pac.URLPath,
		Tailnets:                []tailnetCard{},
		HasUnconfiguredTailnets: registry.HasUnconfiguredTailnets(),
	}

	// Collect all tailnets from registry
	tailnets := registry.List()
	if len(tailnets) == 0 {
		// No tailnets - render empty dashboard
		if err := renderTemplate(w, "dashboard.html", data); err != nil {
			logger.Printf("dashboard: failed to render template: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}

	// Render a card for each tailnet
	for _, tailnet := range tailnets {
		state := tailnet.LatestState()

		bestEffortDomain := "Tailnet"
		if state.MagicDNSSuffix != "" {
			bestEffortDomain = state.MagicDNSSuffix
		}

		hostname := ""
		if state.SelfNode.Valid() {
			hostname = state.SelfNode.ComputedName()
		}

		card := tailnetCard{
			ID:             tailnet.ID(),
			BaseDomain:     bestEffortDomain,
			Hostname:       hostname,
			LifecycleState: string(tailnet.LifecycleState()),
		}

		switch card.LifecycleState {
		case string(ts.LifecycleStarting):
			card.StateClass = "connecting"
			data.Tailnets = append(data.Tailnets, card)
			continue
		case string(ts.LifecycleStopping):
			card.StateClass = "disabling"
			data.Tailnets = append(data.Tailnets, card)
			continue
		case string(ts.LifecycleStopped):
			card.StateClass = "disabled"
			data.Tailnets = append(data.Tailnets, card)
			continue
		default:
			// continue processing
		}

		if state.State == nil {
			card.StateClass = "connecting"
			data.Tailnets = append(data.Tailnets, card)
			continue
		}

		switch *state.State {
		case ipn.NoState:
			card.StateClass = "connecting"
		case ipn.InUseOtherUser:
			// should never happen to us. Consider failure for now.
			logger.Printf("dashboard: unexpected state InUseOtherUser for tailnet")
			card.StateClass = "error"
		case ipn.NeedsLogin:
			// If we don't have the auth URL yet, treat it as still connecting
			if state.BrowseToURL == nil || *state.BrowseToURL == "" {
				card.StateClass = "connecting"
			} else {
				card.StateClass = "needs-login"
				card.AuthURL = *state.BrowseToURL
			}
		case ipn.NeedsMachineAuth:
			card.StateClass = "needs-auth"
		case ipn.Stopped:
			card.StateClass = "disabled"
		case ipn.Starting:
			card.StateClass = "connecting"
		case ipn.Running:
			socksAddr := tailnet.SocksAddr()
			socksHost, socksPort, _ := net.SplitHostPort(socksAddr)
			card.SocksAddr = socksAddr
			card.SocksHost = socksHost
			card.SocksPort = socksPort

			// Add peer machines
			for _, peer := range state.Peers {
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

				card.Machines = append(card.Machines, mv)
			}

			sort.Slice(card.Machines, func(i, j int) bool {
				// Online machines first, then alphabetically by DNS name
				if card.Machines[i].StatusClass != card.Machines[j].StatusClass {
					return card.Machines[i].StatusClass == "online"
				}
				return strings.ToLower(card.Machines[i].DNSName) < strings.ToLower(card.Machines[j].DNSName)
			})

			card.StateClass = "connected"
		default:
			logger.Printf("dashboard: unknown state: %s", state.String())
			card.StateClass = "error"
		}

		data.Tailnets = append(data.Tailnets, card)
	}

	if err := renderTemplate(w, "dashboard.html", data); err != nil {
		logger.Printf("dashboard: failed to render template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}
