package web

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jcambass/tailhopper/internal/logging"
)

// withLoggingContext integrates chi's request ID with your custom logging context.
func withLoggingContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		// chi's RequestID middleware stores the ID under middleware.RequestIDKey
		requestID := middleware.GetReqID(ctx)
		if requestID != "" {
			ctx = logging.WithRequestID(ctx, requestID)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// requestLoggingMiddleware logs HTTP requests and responses with structured logging.
func requestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		slog.InfoContext(ctx, "Request",
			slog.String("component", "httprequests"),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("remote_addr", r.RemoteAddr),
		)

		// Wrap response writer to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		slog.InfoContext(ctx, "Response",
			slog.String("component", "httprequests"),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rw.statusCode),
		)
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code.
// Per Go HTTP spec, if Write is called without WriteHeader, status defaults to 200.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

// WriteHeader captures the status code before writing response headers.
func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

// Write marks the response as written and delegates to the underlying writer.
// Implicitly sets status to 200 if WriteHeader hasn't been called yet.
func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.statusCode = http.StatusOK
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}

// Flush implements http.Flusher, required for SSE and streaming responses.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
