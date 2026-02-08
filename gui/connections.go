package gui

import (
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/jcambass/tailhopper/socks"
	"github.com/jcambass/tailhopper/ts"
)

// HandleConnectionsPartial returns a handler for the connections partial.
func HandleConnectionsPartial(connLog *socks.ConnectionLog, tsServer *ts.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		baseDomain := tsServer.BaseDomain()

		// Get known machines from tsServer
		lc, err := tsServer.LocalClient()
		if err != nil {
			log.Printf("connections partial: failed to get local client: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		status, err := lc.Status(r.Context())
		if err != nil {
			log.Printf("connections partial: failed to get status: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		knownMachines := make(map[string]bool)
		for _, peer := range status.Peer {
			if len(peer.TailscaleIPs) == 0 {
				continue
			}
			machineName := deriveMachineName(peer.DNSName, peer.HostName, baseDomain)
			knownMachines[machineName] = true
		}

		recent, live := connLog.GetRecent(50)
		allGroups := groupAllConnections(recent, live)
		_, unknown := classifyConnectionGroups(allGroups, baseDomain, knownMachines)

		if err := renderTemplate(w, "unknown_connections", unknown); err != nil {
			log.Printf("connections partial: failed to render: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	}
}

// groupAllConnections groups all connections by host:port.
func groupAllConnections(recent []socks.ConnectionEntry, live []socks.ConnectionEntry) []connectionGroupView {
	groupMap := make(map[string]*connectionGroupView)
	groupOrder := make([]string, 0)

	// Process live connections
	for _, lc := range live {
		key := lc.Host + ":" + lc.Port
		group, exists := groupMap[key]
		if !exists {
			group = &connectionGroupView{Host: lc.Host, Port: lc.Port}
			groupMap[key] = group
			groupOrder = append(groupOrder, key)
		}
		group.TotalCount++
		if lc.Connected {
			group.ActiveCount++
		} else {
			group.ConnectingCount++
		}
		group.BytesSent += lc.BytesSent
		group.BytesRecv += lc.BytesRecv
		if lc.StartTime.After(group.LastTime) {
			group.LastTime = lc.StartTime
		}
	}

	// Process completed connections
	for _, c := range recent {
		key := c.Host + ":" + c.Port
		group, exists := groupMap[key]
		if !exists {
			group = &connectionGroupView{Host: c.Host, Port: c.Port}
			groupMap[key] = group
			groupOrder = append(groupOrder, key)
		}
		group.TotalCount++
		group.BytesSent += c.BytesSent
		group.BytesRecv += c.BytesRecv
		if c.Error != "" {
			group.ErrorCount++
		} else {
			group.SuccessCount++
		}
		if c.EndTime.After(group.LastTime) {
			group.LastTime = c.EndTime
		}
	}

	// Build result
	result := make([]connectionGroupView, 0, len(groupOrder))
	for _, key := range groupOrder {
		result = append(result, *groupMap[key])
	}

	// Sort: active/connecting first, then by last time
	sort.Slice(result, func(i, j int) bool {
		iLive := result[i].ActiveCount > 0 || result[i].ConnectingCount > 0
		jLive := result[j].ActiveCount > 0 || result[j].ConnectingCount > 0
		if iLive && !jLive {
			return true
		}
		if jLive && !iLive {
			return false
		}
		return result[i].LastTime.After(result[j].LastTime)
	})

	return result
}

// classifyConnectionGroups splits connection groups into known machine stats and unknown connections.
// This is done at display time so machines can become known/unknown dynamically.
func classifyConnectionGroups(groups []connectionGroupView, baseDomain string, knownMachines map[string]bool) (map[string]*connectionGroupView, []connectionGroupView) {
	known := make(map[string]*connectionGroupView)
	unknown := make([]connectionGroupView, 0)

	for _, g := range groups {
		machineName := ""
		if strings.HasSuffix(g.Host, "."+baseDomain) {
			machineName = strings.TrimSuffix(g.Host, "."+baseDomain)
		}

		if machineName != "" && knownMachines[machineName] {
			// Aggregate into known machine (all ports combined)
			existing, exists := known[machineName]
			if !exists {
				gCopy := g
				gCopy.Host = machineName // Use machine name as host for display
				known[machineName] = &gCopy
			} else {
				existing.TotalCount += g.TotalCount
				existing.ActiveCount += g.ActiveCount
				existing.ConnectingCount += g.ConnectingCount
				existing.SuccessCount += g.SuccessCount
				existing.ErrorCount += g.ErrorCount
				existing.BytesSent += g.BytesSent
				existing.BytesRecv += g.BytesRecv
				if g.LastTime.After(existing.LastTime) {
					existing.LastTime = g.LastTime
				}
			}
		} else {
			unknown = append(unknown, g)
		}
	}

	return known, unknown
}
