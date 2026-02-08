package gui

import (
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/jcambass/tailhopper/portscan"
	"github.com/jcambass/tailhopper/socks"
	"github.com/jcambass/tailhopper/ts"
)

// HandleMachinesPartial returns a handler for the machines partial.
func HandleMachinesPartial(tsServer *ts.Server, scanner *portscan.Scanner, connLog *socks.ConnectionLog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		baseDomain := tsServer.BaseDomain()

		lc, err := tsServer.LocalClient()
		if err != nil {
			http.Error(w, "failed to get local client", http.StatusInternalServerError)
			return
		}

		status, err := lc.Status(r.Context())
		if err != nil {
			http.Error(w, "failed to get status", http.StatusInternalServerError)
			return
		}

		// Build set of known machine names from peers
		knownMachines := make(map[string]bool)
		for _, peer := range status.Peer {
			if len(peer.TailscaleIPs) == 0 {
				continue
			}
			machineName := deriveMachineName(peer.DNSName, peer.HostName, baseDomain)
			knownMachines[machineName] = true
		}

		// Get connection stats by machine
		recent, live := connLog.GetRecent(50)
		allGroups := groupAllConnections(recent, live)
		knownConnections, _ := classifyConnectionGroups(allGroups, baseDomain, knownMachines)

		hostname := ""
		if status.Self != nil {
			hostname = status.Self.HostName
		}

		data := struct {
			Machines []machineView
			Hostname string
		}{
			Machines: []machineView{},
			Hostname: hostname,
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
			if hasPorts && (cachedPorts[0] == 443 || cachedPorts[0] == 8448) {
				defaultHTTPS = true
			}

			mv := machineView{
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
			}

			// Merge connection stats if available
			if stats, ok := knownConnections[machineName]; ok {
				mv.TotalCount = stats.TotalCount
				mv.ActiveCount = stats.ActiveCount
				mv.ConnectingCount = stats.ConnectingCount
				mv.SuccessCount = stats.SuccessCount
				mv.ErrorCount = stats.ErrorCount
				mv.BytesSent = stats.BytesSent
				mv.BytesRecv = stats.BytesRecv
			}

			data.Machines = append(data.Machines, mv)
		}

		sort.Slice(data.Machines, func(i, j int) bool {
			return strings.ToLower(data.Machines[i].DNSName) < strings.ToLower(data.Machines[j].DNSName)
		})

		if err := renderTemplate(w, "machines", data); err != nil {
			log.Printf("machines partial: failed to render: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	}
}
