package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jcambass/tailhopper/internal/pac"
	"github.com/jcambass/tailhopper/internal/registry"
	"github.com/jcambass/tailhopper/internal/sse"
	"github.com/jcambass/tailhopper/internal/ui"
)

// Server represents the HTTP server for Tailhopper.
type Server struct {
	server       *http.Server
	addr         string
	sseBroadcast *sse.SSEBroadcaster
}

// NewServer creates and configures a new HTTP server.
func NewServer(addr string, reg *registry.Registry, broadcaster *sse.SSEBroadcaster) *Server {
	mux := http.NewServeMux()

	// Static files
	mux.Handle("/static/", ui.StaticHandler())

	// Redirects
	mux.HandleFunc("GET /ui/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
	})

	// PAC file
	mux.HandleFunc("GET "+pac.URLPath, pac.Handler(reg))

	// SSE endpoint
	mux.HandleFunc("GET /events", broadcaster.ServeSSE)

	// Dashboard
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		ui.ServeDashboard(w, r, reg, addr)
	})

	// API endpoints
	mux.HandleFunc("POST /api/noop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Tailnet routes
	mux.HandleFunc("POST /tailnet/add", addTailnetHandler(reg, broadcaster))
	mux.HandleFunc("POST /tailnet/{id}/start", tailnetStartHandler(reg))
	mux.HandleFunc("POST /tailnet/{id}/stop", tailnetStopHandler(reg))
	mux.HandleFunc("DELETE /tailnet/{id}", tailnetDeleteHandler(reg, broadcaster))

	handler := withLoggingContext(panicRecoveryMiddleware(requestLoggingMiddleware(mux)))

	return &Server{
		server: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
		},
		addr:         addr,
		sseBroadcast: broadcaster,
	}
}

// Start begins serving HTTP requests (blocking).
func (s *Server) Start() error {
	slog.Info("PAC file available",
		slog.String("component", "httpserver"),
		slog.String("url", fmt.Sprintf("http://%s%s", s.addr, pac.URLPath)),
	)
	slog.Info("Dashboard available",
		slog.String("component", "httpserver"),
		slog.String("url", fmt.Sprintf("http://%s", s.addr)),
	)
	return s.server.ListenAndServe()
}
