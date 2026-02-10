// Package web provides HTTP server setup and routing for Tailhopper.
package web

import (
	"log"
	"net/http"
	"time"

	"github.com/jcambass/tailhopper/internal/pac"
	"github.com/jcambass/tailhopper/internal/ts"
	"github.com/jcambass/tailhopper/internal/ui"
)

// Server represents the HTTP server for Tailhopper.
type Server struct {
	server    *http.Server
	addr      string
	tailnet   *ts.Tailnet
	socksAddr string
}

// NewServer creates and configures a new HTTP server.
func NewServer(addr string, tailnet *ts.Tailnet, socksAddr string) *Server {
	mux := http.NewServeMux()

	// Static files
	mux.Handle("/static/", ui.StaticHandler())

	// Redirects
	mux.Handle("/ui/", http.RedirectHandler("/", http.StatusTemporaryRedirect))

	// PAC file
	mux.Handle(pac.URLPath, pac.Handler(tailnet, socksAddr))

	// Dashboard
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			ui.ServeDashboard(w, r, tailnet, socksAddr)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))

	// Tailnet toggle
	mux.Handle("/tailnet/toggle", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		enabled := r.FormValue("enabled") != ""
		if enabled {
			err := tailnet.Start()
			if err != nil {
				log.Printf("failed to start tailnet: %v", err)
			}
		} else {
			if err := tailnet.Stop(); err != nil {
				log.Printf("failed to disconnect: %v", err)
			}
		}
		w.WriteHeader(http.StatusNoContent)
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
	log.Printf("PAC file available at http://%s%s", s.addr, pac.URLPath)
	log.Printf("Dashboard available at http://%s", s.addr)
	return s.server.ListenAndServe()
}
