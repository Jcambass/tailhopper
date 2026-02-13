// Package logging provides contextual logging helpers using log/slog.
package logging

import (
	"context"
	"log/slog"
	"os"
	"runtime/debug"
)

type requestIDKey struct{}

// WithRequestID stores a request ID in the context.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

// ContextHandler wraps a handler and adds request_id from context to log records.
type ContextHandler struct {
	handler slog.Handler
}

// NewContextHandler creates a new context-aware handler.
func NewContextHandler(h slog.Handler) *ContextHandler {
	return &ContextHandler{handler: h}
}

func (h *ContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if requestID, ok := ctx.Value(requestIDKey{}).(string); ok {
		r.AddAttrs(slog.String("request_id", requestID))
	}
	return h.handler.Handle(ctx, r)
}

func (h *ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ContextHandler{handler: h.handler.WithAttrs(attrs)}
}

func (h *ContextHandler) WithGroup(name string) slog.Handler {
	return &ContextHandler{handler: h.handler.WithGroup(name)}
}

// CatchPanic recovers from panics and logs them.
func CatchPanic(ctx context.Context) {
	if r := recover(); r != nil {
		slog.ErrorContext(ctx, "panic recovered",
			"error", r,
			"stack", string(debug.Stack()),
		)
		os.Exit(1)
	}
}
