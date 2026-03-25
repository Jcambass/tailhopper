package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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
	r := chi.NewRouter()

	// Global middleware stack
	r.Use(middleware.RequestID)     // Built-in: generates request IDs
	r.Use(withLoggingContext)       // Custom: integrates request ID with logging context
	r.Use(middleware.Recoverer)     // Built-in: graceful panic recovery
	r.Use(middleware.RealIP)        // Built-in: extract real IP from headers
	r.Use(requestLoggingMiddleware) // Custom: structured request logging

	// Static files
	r.Handle("/static/*", ui.StaticHandler())

	// Redirects
	r.Get("/ui/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
	})

	// PAC file
	r.Get(pac.URLPath, pac.Handler(reg))

	// SSE endpoint
	r.Get("/events", func(w http.ResponseWriter, r *http.Request) {
		broadcaster.ServeSSE(w, r)
	})

	// Dashboard
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		ui.ServeDashboard(w, r, reg)
	})

	// API endpoints
	r.Post("/api/noop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Tailnet routes
	r.Route("/tailnet", func(r chi.Router) {
		r.Post("/add", addTailnetHandler(reg, broadcaster))
		r.Post("/{id}/start", tailnetStartHandler(reg))
		r.Post("/{id}/stop", tailnetStopHandler(reg))
		r.Delete("/{id}", tailnetDeleteHandler(reg, broadcaster))
	})

	// Handle 404
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		slog.WarnContext(r.Context(), "route not found",
			slog.String("component", "httprequests"),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
		)
		http.Error(w, "not found", http.StatusNotFound)
	})

	// Handle 405 (method not allowed)
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		slog.WarnContext(r.Context(), "method not allowed",
			slog.String("component", "httprequests"),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
		)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})

	return &Server{
		server: &http.Server{
			Addr:              addr,
			Handler:           r,
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
