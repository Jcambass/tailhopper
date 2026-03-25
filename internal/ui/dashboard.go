package ui

import (
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/jcambass/tailhopper/internal/pac"
	"github.com/jcambass/tailhopper/internal/registry"
	"github.com/jcambass/tailhopper/internal/ts"
)

// ServeDashboard renders the main dashboard page.
func ServeDashboard(w http.ResponseWriter, r *http.Request, reg *registry.Registry) {
	ctx := r.Context()

	// Base data structure
	data := dashboardData{
		PACFileURL:              pac.URLPath,
		Tailnets:                []tailnetCard{},
		HasUnconfiguredTailnets: reg.HasUnconfiguredTailnets(),
		HasTailnets:             false,
	}

	// Collect all tailnets from registry
	tailnets := reg.List()
	if len(tailnets) == 0 {
		// No tailnets - render empty dashboard
		if err := renderTemplate(w, "dashboard.html", data); err != nil {
			slog.ErrorContext(ctx, "dashboard: failed to render template", slog.String("component", "dashboard"), slog.Any("error", err))
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}

	// Mark that we have tailnets
	data.HasTailnets = true

	// Render a card for each tailnet
	for _, tailnet := range tailnets {

		snapshot := tailnet.Snapshot()
		bestEffortDomain := "Tailnet"
		tailnetMagicDNSSuffix := snapshot.MagicDNSSuffix
		if tailnetMagicDNSSuffix != "" {
			bestEffortDomain = tailnetMagicDNSSuffix
		}

		card := tailnetCard{
			ID:         tailnet.ID(),
			BaseDomain: bestEffortDomain,
			stateName:  snapshot.State,
			userState:  snapshot.UserState,
			Hostname:   snapshot.Hostname,
		}

		if snapshot.State == ts.HasTerminalErrorState {
			terminalErr := snapshot.TerminalError
			if terminalErr == "" {
				terminalErr = "unknown error"
			}
			card.ErrorMsg = terminalErr

			data.Tailnets = append(data.Tailnets, card)
			continue
		}

		if snapshot.State == ts.NeedsLoginState {
			card.AuthURL = snapshot.LoginURL
			data.Tailnets = append(data.Tailnets, card)
			continue
		}

		if snapshot.State == ts.ConnectedState {
			socksAddr := tailnet.SocksAddr()
			socksHost, socksPort, _ := net.SplitHostPort(socksAddr)
			card.SocksAddr = socksAddr
			card.SocksHost = socksHost
			card.SocksPort = socksPort

			peers := snapshot.Peers
			// Add peer machines
			for _, peer := range peers {
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

			card.stateName = ts.ConnectedState

			data.Tailnets = append(data.Tailnets, card)
			continue
		}

		// For all other states it's simple
		data.Tailnets = append(data.Tailnets, card)
	}

	if err := renderTemplate(w, "dashboard.html", data); err != nil {
		slog.ErrorContext(ctx, "dashboard: failed to render template", slog.String("component", "dashboard"), slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}
