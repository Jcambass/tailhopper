// Package web provides HTTP server setup and routing for Tailhopper.
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/jcambass/tailhopper/internal/logging"
	"github.com/jcambass/tailhopper/internal/pac"
	"github.com/jcambass/tailhopper/internal/ts"
	"github.com/jcambass/tailhopper/internal/ui"
)

// Server represents the HTTP server for Tailhopper.
type Server struct {
	server *http.Server
	addr   string
	logger *logging.Logger
}

// NewServer creates and configures a new HTTP server.
func NewServer(addr string, tailnet *ts.Tailnet, socksAddr string) *Server {
	logger := logging.Default().With("component", "httpserver")
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
		requestLogger := logging.FromContext(r.Context())
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
			err := tailnet.Start(r.Context())
			if err != nil {
				requestLogger.Printf("failed to start tailnet: %v", err)
			}
		} else {
			go func() {
				ctx := logging.WithContext(context.Background(), requestLogger)
				if err := tailnet.Stop(ctx); err != nil {
					requestLogger.Printf("failed to disconnect: %v", err)
				}
			}()
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	rootHandler := withRequestContext(withRecovery(mux))

	return &Server{
		server: &http.Server{
			Addr:              addr,
			Handler:           rootHandler,
			ReadHeaderTimeout: 10 * time.Second,
		},
		addr:   addr,
		logger: logger,
	}
}

// Start begins serving HTTP requests (blocking).
func (s *Server) Start() error {
	s.logger.Printf("PAC file available at http://%s%s", s.addr, pac.URLPath)
	s.logger.Printf("Dashboard available at http://%s", s.addr)
	return s.server.ListenAndServe()
}

type requestIDKey struct{}

func withRequestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-Id", requestID)

		logger := logging.Default().WithFields(map[string]string{
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
