package gui

import (
	"log"
	"net/http"

	"github.com/jcambass/tailhopper/socks"
)

// HandleConnectionsPartial returns a handler for the connections partial.
func HandleConnectionsPartial(connLog *socks.ConnectionLog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		recent, live := connLog.GetRecent(20)

		connections := make([]connectionView, 0, len(live)+len(recent))
		for _, lc := range live {
			connections = append(connections, connectionView{
				Host:      lc.Host,
				Port:      lc.Port,
				StartTime: lc.StartTime,
				BytesSent: lc.BytesSent,
				BytesRecv: lc.BytesRecv,
				Active:    true,
			})
		}
		for _, c := range recent {
			connections = append(connections, connectionView{
				Host:      c.Host,
				Port:      c.Port,
				StartTime: c.StartTime,
				EndTime:   c.EndTime,
				BytesSent: c.BytesSent,
				BytesRecv: c.BytesRecv,
				Error:     c.Error,
			})
		}

		if err := renderTemplate(w, "connections", connections); err != nil {
			log.Printf("connections partial: failed to render: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	}
}
