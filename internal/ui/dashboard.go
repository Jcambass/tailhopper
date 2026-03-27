package ui

import (
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/jcambass/tailhopper/internal/pac"
	"github.com/jcambass/tailhopper/internal/registry"
	"github.com/jcambass/tailhopper/internal/tailscale"
	"tailscale.com/tailcfg"
)

// ServeDashboard renders the main dashboard page.
func ServeDashboard(w http.ResponseWriter, r *http.Request, reg *registry.Registry, listenAddr string) {
	ctx := r.Context()

	data := dashboardData{
		PACFileURL:              "http://" + listenAddr + pac.URLPath,
		Tailnets:                []tailnetCard{},
		HasUnconfiguredTailnets: reg.HasUnconfiguredTailnets(),
	}

	tailnets := reg.List()
	data.HasTailnets = len(tailnets) > 0

	for _, t := range tailnets {
		data.Tailnets = append(data.Tailnets, buildTailnetCard(t))
	}

	if err := renderTemplate(w, "dashboard.html", data); err != nil {
		slog.ErrorContext(ctx, "dashboard: failed to render template", slog.String("component", "dashboard"), slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// buildTailnetCard creates a tailnetCard from a Tailnet, populating
// state-specific fields (error message, auth URL, machines) as needed.
func buildTailnetCard(t *tailscale.Tailnet) tailnetCard {
	snapshot := t.Snapshot()

	baseDomain := "Tailnet"
	if snapshot.MagicDNSSuffix != "" {
		baseDomain = snapshot.MagicDNSSuffix
	}

	card := tailnetCard{
		ID:         t.ID(),
		BaseDomain: baseDomain,
		stateName:  snapshot.State,
		userState:  snapshot.UserState,
		Hostname:   snapshot.Hostname,
	}

	switch snapshot.State {
	case tailscale.HasTerminalErrorState:
		card.ErrorMsg = snapshot.TerminalError
		if card.ErrorMsg == "" {
			card.ErrorMsg = "unknown error"
		}

	case tailscale.NeedsLoginState:
		card.AuthURL = snapshot.LoginURL

	case tailscale.ConnectedState:
		socksAddr := t.SocksAddr()
		host, port, _ := net.SplitHostPort(socksAddr)
		card.SocksAddr = socksAddr
		card.SocksHost = host
		card.SocksPort = port
		card.Machines = buildMachineViews(snapshot.Peers)
	}

	return card
}

// buildMachineViews converts peer nodes into sorted display models.
// Online machines come first, then alphabetically by DNS name.
func buildMachineViews(peers []tailcfg.NodeView) []machineView {
	var machines []machineView

	for _, peer := range peers {
		if peer.Addresses().Len() == 0 {
			continue
		}

		statusClass, statusText := "offline", "offline"
		if peer.Online().Get() {
			statusClass, statusText = "online", "online"
		}

		machines = append(machines, machineView{
			Name:        peer.ComputedName(),
			DNSName:     peer.Name(),
			StatusClass: statusClass,
			StatusText:  statusText,
			IPs:         strings.Join(formatIPs(peer.Addresses().AsSlice()), ", "),
		})
	}

	sort.Slice(machines, func(i, j int) bool {
		if machines[i].StatusClass != machines[j].StatusClass {
			return machines[i].StatusClass == "online"
		}
		return strings.ToLower(machines[i].DNSName) < strings.ToLower(machines[j].DNSName)
	})

	return machines
}
