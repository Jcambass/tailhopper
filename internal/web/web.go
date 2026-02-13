// Package web provides HTTP server setup and routing for Tailhopper.
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jcambass/tailhopper/internal/logging"
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
	mux.Handle("/ui/", http.RedirectHandler("/", http.StatusTemporaryRedirect))

	// PAC file
	mux.Handle(pac.URLPath, withRequestLogging(pac.Handler(reg)))

	// SSE endpoint
	mux.Handle("/events", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		broadcaster.ServeSSE(w, r)
	}))

	// Dashboard
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			ui.ServeDashboard(w, r, reg)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))

	// Noop endpoint for htmx triggers
	mux.Handle("/api/noop", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Tailnet handlers - must be registered before the general /tailnet/ pattern
	mux.Handle("/tailnet/add", withRequestLogging(createAddTailnetHandler(reg, broadcaster)))
	mux.Handle("/tailnet/", withRequestLogging(createTailnetHandler(reg, broadcaster)))

	rootHandler := withRequestContext(withRecovery(mux))

	return &Server{
		server: &http.Server{
			Addr:              addr,
			Handler:           rootHandler,
			ReadHeaderTimeout: 10 * time.Second,
		},
		addr:         addr,
		sseBroadcast: broadcaster,
	}
}

// Start begins serving HTTP requests (blocking).
func (s *Server) Start() error {
	slog.Info("PAC file available", "component", "httpserver", "url", fmt.Sprintf("http://%s%s", s.addr, pac.URLPath))
	slog.Info("Dashboard available", "component", "httpserver", "url", fmt.Sprintf("http://%s", s.addr))
	return s.server.ListenAndServe()
}

// createTailnetHandler returns an HTTP handler for tailnet start/stop/delete operations.
// Path format: /tailnet/{id}/start, /tailnet/{id}/stop, or /tailnet/{id} (DELETE)
func createTailnetHandler(reg *registry.Registry, broadcaster *sse.SSEBroadcaster) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Extract tailnet ID from path
		pathWithoutPrefix := strings.TrimPrefix(r.URL.Path, "/tailnet/")
		parts := strings.Split(pathWithoutPrefix, "/")

		if len(parts) == 0 || parts[0] == "" {
			http.Error(w, "missing tailnet id", http.StatusBadRequest)
			return
		}

		idStr := parts[0]

		// convert it to int
		id, err := strconv.Atoi(idStr)
		if err != nil {
			slog.WarnContext(ctx, "invalid tailnet id", "component", "httprequests", "id", idStr)
			http.Error(w, "invalid tailnet id", http.StatusBadRequest)
			return
		}

		// Handle DELETE /tailnet/{id}
		if r.Method == http.MethodDelete {
			if len(parts) != 1 {
				http.Error(w, "invalid path", http.StatusNotFound)
				return
			}

			var logoutErr error

			// Try to logout and stop the tailnet

			tailnet, ok := reg.Get(id)

			if !ok {
				slog.WarnContext(ctx, "component", "httprequests", "tailnet not found", "id", id)
				http.Error(w, "tailnet not found", http.StatusNotFound)
				return
			}

			// Give logout a deadline to prevent hanging the request indefinitely.
			// Note that logout might internally starts the tsnet server if it's not already started, so we need to give it enough time to do that and complete the logout process.
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()

			err := tailnet.Logout(ctx)
			if err != nil {
				slog.ErrorContext(r.Context(), "component", "httprequests", "logout failed", "id", id, "error", err)
				logoutErr = err
			} else {
				slog.InfoContext(r.Context(), "component", "httprequests", "logout succeeded", "id", id)
			}

			// If logout failed, we log it but still continue to delete the tailnet locally.

			// Always delete regardless of logout success
			if err := reg.Delete(id); err != nil {
				slog.ErrorContext(r.Context(), "component", "httprequests", "failed to delete tailnet", "error", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			slog.InfoContext(r.Context(), "tailnet deleted successfully", "component", "httprequests")
			broadcaster.BroadcastGlobalChange()

			// Return toast HTML using OOB swap with htmx auto-removal
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)

			var message, toastType string
			if logoutErr == nil {
				message = "Tailnet deleted and logged out successfully"
				toastType = "success"
			} else {
				message = fmt.Sprintf("Tailnet deleted, but logout failed: %s", logoutErr.Error())
				toastType = "warning"
			}

			toastHTML, err := ui.RenderToast(toastType, message)
			if err != nil {
				slog.ErrorContext(r.Context(), "component", "httprequests", "failed to render toast", "error", err)
				return
			}
			fmt.Fprint(w, toastHTML)
			return
		}

		// Handle POST /tailnet/{id}/start or /tailnet/{id}/stop
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if len(parts) != 2 {
			http.Error(w, "invalid path", http.StatusNotFound)
			return
		}

		action := parts[1]

		if action != "start" && action != "stop" {
			http.Error(w, "invalid action, must be 'start' or 'stop'", http.StatusBadRequest)
			return
		}

		tailnet, ok := reg.Get(id)
		if !ok {
			http.Error(w, "tailnet not found", http.StatusNotFound)
			return
		}

		if action == "start" {
			if err := tailnet.Start(r.Context()); err != nil {
				slog.ErrorContext(r.Context(), "component", "httprequests", "failed to start tailnet", "error", err)
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		} else { // stop
			if err := tailnet.Stop(r.Context()); err != nil {
				slog.ErrorContext(r.Context(), "component", "httprequests", "failed to stop tailnet", "error", err)
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}

		// Note: SSE broadcasts state changes.
	})
}

// createAddTailnetHandler returns an HTTP handler for creating a new tailnet.
func createAddTailnetHandler(reg *registry.Registry, sseBroadcast *sse.SSEBroadcaster) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Check if there are any unconfigured tailnets
		if reg.HasUnconfiguredTailnets() {
			slog.WarnContext(ctx, "component", "httprequests", "cannot add new tailnet: unconfigured tailnet exists")
			http.Error(w, "cannot create new tailnet while an existing tailnet is unconfigured", http.StatusConflict)
			return
		}

		tailnet, err := reg.Add("") // Empty hostname will be auto-generated by registry
		if err != nil {
			slog.ErrorContext(ctx, "component", "httprequests", "failed to add tailnet", "error", err)
			http.Error(w, fmt.Sprintf("failed to add tailnet: %v", err), http.StatusBadRequest)
			return
		}

		// Automatically start the new tailnet
		if err := tailnet.Start(r.Context()); err != nil {
			slog.ErrorContext(ctx, "component", "httprequests", "failed to start new tailnet", "error", err)
			// Don't fail the request, the tailnet was created successfully
		}

		// Notify about new tailnet (registry also sends notification, but this ensures immediate update)
		sseBroadcast.BroadcastGlobalChange()

		w.WriteHeader(http.StatusCreated)
	})
}

type requestIDKey struct{}

func withRequestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-Id", requestID)

		ctx := logging.WithRequestID(r.Context(), requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(buffer)
}

func withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer logging.CatchPanic(r.Context())
		next.ServeHTTP(w, r)
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.statusCode = http.StatusOK
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}

func withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		slog.InfoContext(ctx, "Request", "component", "httprequests", "method", r.Method, "path", r.URL.Path, "remote_addr", r.RemoteAddr)

		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		slog.InfoContext(ctx, "component", "httprequests", "Response", "method", r.Method, "path", r.URL.Path, "status", rw.statusCode)
	})
}
