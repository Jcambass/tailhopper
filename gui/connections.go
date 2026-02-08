package gui

import (
	"log"
	"net/http"
	"sort"

	"github.com/jcambass/tailhopper/socks"
)

// HandleConnectionsPartial returns a handler for the connections partial.
func HandleConnectionsPartial(connLog *socks.ConnectionLog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		recent, live := connLog.GetRecent(50)
		groups := groupConnections(recent, live)

		if err := renderTemplate(w, "connection_groups", groups); err != nil {
			log.Printf("connections partial: failed to render: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	}
}

// groupConnections groups connections by host:port and calculates aggregates.
func groupConnections(recent []socks.ConnectionEntry, live []socks.ConnectionEntry) []connectionGroupView {
	groupMap := make(map[string]*connectionGroupView)
	keyOrder := make([]string, 0)

	// Add live connections
	for _, lc := range live {
		key := lc.Host + ":" + lc.Port
		group, exists := groupMap[key]
		if !exists {
			group = &connectionGroupView{
				Host: lc.Host,
				Port: lc.Port,
			}
			groupMap[key] = group
			keyOrder = append(keyOrder, key)
		}

		group.TotalCount++
		group.ActiveCount++
		group.BytesSent += lc.BytesSent
		group.BytesRecv += lc.BytesRecv
		if lc.StartTime.After(group.LastTime) {
			group.LastTime = lc.StartTime
		}
	}

	// Add completed connections
	for _, c := range recent {
		key := c.Host + ":" + c.Port
		group, exists := groupMap[key]
		if !exists {
			group = &connectionGroupView{
				Host: c.Host,
				Port: c.Port,
			}
			groupMap[key] = group
			keyOrder = append(keyOrder, key)
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

	// Build result in order
	result := make([]connectionGroupView, 0, len(keyOrder))
	for _, key := range keyOrder {
		result = append(result, *groupMap[key])
	}

	// Sort: active first, then by last time
	sort.Slice(result, func(i, j int) bool {
		if result[i].ActiveCount > 0 && result[j].ActiveCount == 0 {
			return true
		}
		if result[j].ActiveCount > 0 && result[i].ActiveCount == 0 {
			return false
		}
		return result[i].LastTime.After(result[j].LastTime)
	})

	return result
}
