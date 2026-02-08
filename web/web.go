// Package web provides HTTP server setup and routing for Tailhopper.
package web

import (
	"log"
	"net/http"
	"time"

	"github.com/jcambass/tailhopper/gui"
	"github.com/jcambass/tailhopper/pac"
	"github.com/jcambass/tailhopper/socks"
	"github.com/jcambass/tailhopper/ts"
)

// Server represents the HTTP server for Tailhopper.
type Server struct {
	server    *http.Server
	addr      string
	tsServer  *ts.Server
	socksAddr string
	connLog   *socks.ConnectionLog
}

// NewServer creates and configures a new HTTP server.
func NewServer(addr string, tsServer *ts.Server, socksAddr string, connLog *socks.ConnectionLog) *Server {
	mux := http.NewServeMux()

	// Static files
	mux.Handle("/static/", gui.StaticHandler())

	// Redirects
	mux.Handle("/ui/", http.RedirectHandler("/", http.StatusTemporaryRedirect))

	// Partial endpoints for htmx
	mux.Handle("/partials/connections", gui.HandleConnectionsPartial(connLog, tsServer))
	mux.Handle("/partials/machines", gui.HandleMachinesPartial(tsServer, connLog))

	// PAC file - uses BaseDomainGetter interface
	mux.Handle(pac.URLPath, pac.Handler(tsServer, socksAddr))

	// Dashboard
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			gui.ServeDashboard(w, r, tsServer, socksAddr, connLog)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))

	return &Server{
		server: &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		},
		addr: addr,
	}
}

// Start begins serving HTTP requests (blocking).
func (s *Server) Start() error {
	log.Printf("HTTP server listening on http://%s", s.addr)
	log.Printf("PAC file available at http://%s%s", s.addr, pac.URLPath)
	log.Printf("Dashboard available at http://%s/", s.addr)
	return s.server.ListenAndServe()
}
