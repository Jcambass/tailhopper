// Package logging provides contextual logging helpers using log/slog.
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
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

// SimplifySource returns a ReplaceAttr function that formats source locations
// to show only the path relative to the project root.
// It also renames the field to "caller" which hl recognizes for arrow formatting.
func SimplifySource() func([]string, slog.Attr) slog.Attr {
	return func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.SourceKey {
			if src, ok := a.Value.Any().(*slog.Source); ok {
				// Remove everything before "/tailhopper/"
				path := src.File
				if idx := strings.Index(path, "/tailhopper/"); idx >= 0 {
					path = path[idx+12:]
				}
				// Rename to "caller" for hl to format with arrow
				a.Key = "caller"
				a.Value = slog.StringValue(fmt.Sprintf("%s:%d", path, src.Line))
			}
		}
		return a
	}
}

// CatchPanic recovers from panics and logs them.
func CatchPanic(ctx context.Context) {
	if r := recover(); r != nil {
		slog.ErrorContext(ctx, "panic recovered",
			slog.Any("error", fmt.Sprintf("%v", r)),
			slog.String("stack", string(debug.Stack())),
		)
		os.Exit(1)
	}
}
