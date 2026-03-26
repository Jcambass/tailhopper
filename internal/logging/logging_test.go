package logging

import (
	"context"
	"log/slog"
	"testing"
)

func TestWithRequestID_RoundTrip(t *testing.T) {
	ctx := context.Background()
	ctx = WithRequestID(ctx, "req-123")

	id, ok := ctx.Value(requestIDKey{}).(string)
	if !ok {
		t.Fatal("expected request ID in context")
	}
	if id != "req-123" {
		t.Errorf("got %q, want %q", id, "req-123")
	}
}

func TestWithRequestID_EmptyContext(t *testing.T) {
	ctx := context.Background()
	_, ok := ctx.Value(requestIDKey{}).(string)
	if ok {
		t.Error("expected no request ID in empty context")
	}
}

func TestContextHandler_Enabled(t *testing.T) {
	inner := slog.Default().Handler()
	handler := NewContextHandler(inner)

	_ = handler.Enabled(context.Background(), slog.LevelInfo)
}

func TestContextHandler_Handle(t *testing.T) {
	inner := slog.Default().Handler()
	handler := NewContextHandler(inner)

	r := slog.Record{}
	r.AddAttrs(slog.String("test", "value"))
	err := handler.Handle(context.Background(), r)
	if err != nil {
		t.Fatalf("Handle without request ID: %v", err)
	}

	ctx := WithRequestID(context.Background(), "req-456")
	err = handler.Handle(ctx, r)
	if err != nil {
		t.Fatalf("Handle with request ID: %v", err)
	}
}

func TestContextHandler_WithAttrs(t *testing.T) {
	inner := slog.Default().Handler()
	handler := NewContextHandler(inner)

	wrapped := handler.WithAttrs([]slog.Attr{slog.String("key", "value")})
	if wrapped == nil {
		t.Fatal("expected non-nil handler from WithAttrs")
	}
	if _, ok := wrapped.(*ContextHandler); !ok {
		t.Error("expected ContextHandler type")
	}
}

func TestContextHandler_WithGroup(t *testing.T) {
	inner := slog.Default().Handler()
	handler := NewContextHandler(inner)

	wrapped := handler.WithGroup("group")
	if wrapped == nil {
		t.Fatal("expected non-nil handler from WithGroup")
	}
	if _, ok := wrapped.(*ContextHandler); !ok {
		t.Error("expected ContextHandler type")
	}
}

func TestSimplifySource(t *testing.T) {
	fn := SimplifySource()

	tests := []struct {
		name    string
		attr    slog.Attr
		wantKey string
	}{
		{
			name: "source attribute",
			attr: slog.Any(slog.SourceKey, &slog.Source{
				File: "/home/user/projects/tailhopper/internal/web/server.go",
				Line: 42,
			}),
			wantKey: "caller",
		},
		{
			name:    "non-source attribute",
			attr:    slog.String("key", "value"),
			wantKey: "key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fn(nil, tt.attr)
			if result.Key != tt.wantKey {
				t.Errorf("key = %q, want %q", result.Key, tt.wantKey)
			}
		})
	}
}

func TestSimplifySource_Format(t *testing.T) {
	fn := SimplifySource()

	attr := slog.Any(slog.SourceKey, &slog.Source{
		File: "/home/user/projects/tailhopper/internal/web/server.go",
		Line: 42,
	})
	result := fn(nil, attr)

	if result.Value.String() != "internal/web/server.go:42" {
		t.Errorf("got %q, want %q", result.Value.String(), "internal/web/server.go:42")
	}
}
