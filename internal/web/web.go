// Package web provides HTTP server setup and routing for Tailhopper.
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jcambass/tailhopper/internal/logging"
	"github.com/jcambass/tailhopper/internal/pac"
	"github.com/jcambass/tailhopper/internal/sse"
	"github.com/jcambass/tailhopper/internal/ts"
	"github.com/jcambass/tailhopper/internal/ui"
)

// Server represents the HTTP server for Tailhopper.
type Server struct {
	server       *http.Server
	addr         string
	logger       *logging.Logger
	sseBroadcast *sse.SSEBroadcaster
}

// NewServer creates and configures a new HTTP server.
func NewServer(addr string, registry *ts.Registry, broadcaster *sse.SSEBroadcaster) *Server {
	logger := logging.Default().With("component", "httpserver")
	mux := http.NewServeMux()

	// Static files
	mux.Handle("/static/", ui.StaticHandler())

	// Redirects
	mux.Handle("/ui/", http.RedirectHandler("/", http.StatusTemporaryRedirect))

	// PAC file
	mux.Handle(pac.URLPath, withRequestLogging(pac.Handler(registry)))

	// SSE endpoint
	mux.Handle("/events", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		broadcaster.ServeSSE(w, r)
	}))

	// Dashboard
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			ui.ServeDashboard(w, r, registry)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))

	// Noop endpoint for htmx triggers
	mux.Handle("/api/noop", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Tailnet handlers - must be registered before the general /tailnet/ pattern
	mux.Handle("/tailnet/add", withRequestLogging(createAddTailnetHandler(registry, broadcaster)))
	mux.Handle("/tailnet/", withRequestLogging(createTailnetHandler(registry, broadcaster)))

	rootHandler := withRequestContext(withRecovery(mux))

	return &Server{
		server: &http.Server{
			Addr:              addr,
			Handler:           rootHandler,
			ReadHeaderTimeout: 10 * time.Second,
		},
		addr:         addr,
		logger:       logger,
		sseBroadcast: broadcaster,
	}
}

// Start begins serving HTTP requests (blocking).
func (s *Server) Start() error {
	s.logger.Printf("PAC file available at http://%s%s", s.addr, pac.URLPath)
	s.logger.Printf("Dashboard available at http://%s", s.addr)
	return s.server.ListenAndServe()
}

// createTailnetHandler returns an HTTP handler for tailnet start/stop/delete operations.
// Path format: /tailnet/{id}/start, /tailnet/{id}/stop, or /tailnet/{id} (DELETE)
func createTailnetHandler(registry *ts.Registry, broadcaster *sse.SSEBroadcaster) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger := logging.FromContext(r.Context())

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
			logger.Printf("invalid tailnet id: %s", id)
			http.Error(w, "invalid tailnet id", http.StatusBadRequest)
			return
		}

		// Handle DELETE /tailnet/{id}
		if r.Method == http.MethodDelete {
			if len(parts) != 1 {
				http.Error(w, "invalid path", http.StatusNotFound)
				return
			}

			logoutSucceeded := false
			var logoutErr error

			// TODO: BROKEN This does not handle starting and stopping states.
			// It also does not handle all other sub states we now have.
			// Let's consider adding something to the states themsels to handle this logic!
			// Ideally we add a new state for this situation that startups temporarly and logsout and than transitions to stopped again.

			// Try to logout and stop the tailnet
			if tailnet, ok := registry.Get(id); ok {
				// If stopped, try to start it temporarily for logout
				if tailnet.StateName() == ts.StoppedStateName {
					logger.Printf("attempting to start stopped tailnet for logout")
					if err := tailnet.Start(r.Context()); err != nil {
						logger.Printf("failed to start tailnet for logout: %v", err)
						logoutErr = fmt.Errorf("could not start tailnet for logout: %v", err)
					} else {
						// Wait for tailnet to initialize
						// Poll for up to 3 seconds to see if it reaches started state
						startSuccess := false
						for i := 0; i < 6; i++ {
							time.Sleep(500 * time.Millisecond)
							if tailnet.StateName() == ts.StartedStateName {
								startSuccess = true
								break
							}
						}
						if !startSuccess {
							logger.Printf("tailnet did not reach started state in time")
							logoutErr = fmt.Errorf("tailnet startup timeout")
						}
					}
				}

				// Try to logout if we have a running tailnet
				if tailnet.StateName() == ts.StartedStateName {
					if err := tailnet.Logout(r.Context()); err != nil {
						logger.Printf("failed to logout from tailnet: %v", err)
						logoutErr = err
					} else {
						logger.Printf("successfully logged out from tailnet")
						logoutSucceeded = true
					}
				}

				// Stop the tailnet if it's running
				if tailnet.StateName() != ts.StoppedStateName {
					if err := tailnet.Stop(r.Context()); err != nil {
						logger.Printf("failed to stop tailnet before deletion: %v", err)
						// Continue with deletion anyway
					}
				}
			}

			// Always delete regardless of logout success
			if err := registry.Delete(id); err != nil {
				logger.Printf("failed to delete tailnet: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			logger.Printf("tailnet deleted successfully (logout: %v)", logoutSucceeded)
			broadcaster.BroadcastGlobalChange()

			// Return toast HTML using OOB swap with htmx auto-removal
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)

			var message, toastType string
			if logoutSucceeded {
				message = "Tailnet deleted and logged out successfully"
				toastType = "success"
			} else if logoutErr != nil {
				message = fmt.Sprintf("Tailnet deleted, but logout failed: %s", logoutErr.Error())
				toastType = "warning"
			} else {
				message = "Tailnet deleted (logout skipped - tailnet was not running)"
				toastType = "info"
			}

			toastHTML, err := ui.RenderToast(toastType, message)
			if err != nil {
				logger.Printf("failed to render toast: %v", err)
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

		tailnet, ok := registry.Get(id)
		if !ok {
			http.Error(w, "tailnet not found", http.StatusNotFound)
			return
		}

		if action == "start" {
			if err := tailnet.Start(r.Context()); err != nil {
				logger.Printf("failed to start tailnet: %v", err)
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		} else { // stop
			if err := tailnet.Stop(r.Context()); err != nil {
				logger.Printf("failed to stop tailnet: %v", err)
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}

		// Note: SSE broadcasts state changes.
	})
}

// createAddTailnetHandler returns an HTTP handler for creating a new tailnet.
func createAddTailnetHandler(registry *ts.Registry, sseBroadcast *sse.SSEBroadcaster) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger := logging.FromContext(r.Context())

		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Check if there are any unconfigured tailnets
		if registry.HasUnconfiguredTailnets() {
			logger.Printf("cannot add new tailnet: unconfigured tailnet exists")
			http.Error(w, "cannot create new tailnet while an existing tailnet is unconfigured", http.StatusConflict)
			return
		}

		tailnet, err := registry.Add("") // Empty hostname will be auto-generated by registry
		if err != nil {
			logger.Printf("failed to add tailnet: %v", err)
			http.Error(w, fmt.Sprintf("failed to add tailnet: %v", err), http.StatusBadRequest)
			return
		}

		// Automatically start the new tailnet
		if err := tailnet.Start(r.Context()); err != nil {
			logger.Printf("failed to start new tailnet: %v", err)
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

		logger := logging.Default().WithFields(map[string]any{
			"component":  "httprequests",
			"request_id": requestID,
		})
		ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
		ctx = logging.WithContext(ctx, logger)
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
		logger := logging.FromContext(r.Context())
		defer logging.CatchPanic(logger)
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
		logger := logging.FromContext(r.Context())
		logger.Printf("Request: %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)

		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		logger.Printf("Response: %s %s -> %d", r.Method, r.URL.Path, rw.statusCode)
	})
}
