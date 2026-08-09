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
	"github.com/jcambass/tailhopper/internal/ui"
)

// ListenAndServe configures and runs the Tailhopper HTTP server.
func ListenAndServe(addr string, reg *registry.Registry) error {
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

	// Dashboard
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		ui.ServeDashboard(w, r, reg, addr)
	})

	// API endpoints
	r.Post("/api/noop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Tailnet routes
	r.Route("/tailnet", func(r chi.Router) {
		r.Post("/add", addTailnetHandler(reg))
		r.Post("/{id}/start", tailnetStartHandler(reg))
		r.Post("/{id}/stop", tailnetStopHandler(reg))
		r.Delete("/{id}", tailnetDeleteHandler(reg))
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

	slog.Info("PAC file available",
		slog.String("component", "httpserver"),
		slog.String("url", fmt.Sprintf("http://%s%s", addr, pac.URLPath)),
	)
	slog.Info("Dashboard available",
		slog.String("component", "httpserver"),
		slog.String("url", fmt.Sprintf("http://%s", addr)),
	)
	return (&http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}).ListenAndServe()
}
