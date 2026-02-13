package ui

import (
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/jcambass/tailhopper/internal/logging"
	"github.com/jcambass/tailhopper/internal/pac"
	"github.com/jcambass/tailhopper/internal/ts"
)

// ServeDashboard renders the main dashboard page.
func ServeDashboard(w http.ResponseWriter, r *http.Request, registry *ts.Registry) {
	logger := logging.FromContext(r.Context()).With("component", "dashboard")

	// Base data structure
	data := dashboardData{
		PACFileURL:              pac.URLPath,
		Tailnets:                []tailnetCard{},
		HasUnconfiguredTailnets: registry.HasUnconfiguredTailnets(),
		HasTailnets:             false,
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

	// Mark that we have tailnets
	data.HasTailnets = true

	// Render a card for each tailnet
	for _, tailnet := range tailnets {

		bestEffortDomain := "Tailnet"
		tailnetMagicDNSSuffix := tailnet.MagicDNSSuffix()
		if tailnetMagicDNSSuffix != "" {
			bestEffortDomain = tailnetMagicDNSSuffix
		}

		card := tailnetCard{
			ID:         tailnet.ID(),
			BaseDomain: bestEffortDomain,
			stateName:  tailnet.StateName(),
			Hostname:   tailnet.Hostname(),
		}

		if tailnet.StateName() == ts.HasTerminalErrorStateName {
			terminalErr, err := tailnet.TerminalError()
			if err != nil {
				panic("unexpected error getting terminal error for tailnet in error state: " + err.Error())
			}
			card.ErrorMsg = terminalErr

			data.Tailnets = append(data.Tailnets, card)
			continue
		}

		if tailnet.StateName() == ts.NeedsLoginStateName {
			loginURL, err := tailnet.LoginURL()
			if err != nil {
				panic("unexpected error getting login URL for tailnet in needs-login state: " + err.Error())
			}
			card.AuthURL = loginURL
			data.Tailnets = append(data.Tailnets, card)
			continue
		}

		if tailnet.StateName() == ts.ConnectedStateName {
			socksAddr := tailnet.SocksAddr()
			socksHost, socksPort, _ := net.SplitHostPort(socksAddr)
			card.SocksAddr = socksAddr
			card.SocksHost = socksHost
			card.SocksPort = socksPort

			peers, err := tailnet.Peers()
			if err != nil {
				panic("unexpected error getting peers for tailnet in connected state: " + err.Error())
			}
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

			card.stateName = ts.ConnectedStateName

			data.Tailnets = append(data.Tailnets, card)
			continue
		}

		// For all other states it's simple
		data.Tailnets = append(data.Tailnets, card)
	}

	if err := renderTemplate(w, "dashboard.html", data); err != nil {
		logger.Printf("dashboard: failed to render template: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}
