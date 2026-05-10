package logging

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
)

func TestWithRequestID(t *testing.T) {
	ctx := context.Background()
	ctx = WithRequestID(ctx, "req-123")

	val, ok := ctx.Value(requestIDKey{}).(string)
	if !ok {
		t.Fatal("expected request ID in context")
	}
	if val != "req-123" {
		t.Errorf("got %q, want %q", val, "req-123")
	}
}

func TestContextHandler_AddsRequestID(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{})
	handler := NewContextHandler(base)
	logger := slog.New(handler)

	ctx := WithRequestID(context.Background(), "test-id")
	logger.InfoContext(ctx, "hello")

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("request_id=test-id")) {
		t.Errorf("expected request_id in output, got %q", output)
	}
}

func TestContextHandler_NoRequestID(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{})
	handler := NewContextHandler(base)
	logger := slog.New(handler)

	logger.Info("hello")

	output := buf.String()
	if bytes.Contains([]byte(output), []byte("request_id")) {
		t.Errorf("unexpected request_id in output: %q", output)
	}
}

func TestContextHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{})
	handler := NewContextHandler(base)

	// WithAttrs should return a ContextHandler
	h2 := handler.WithAttrs([]slog.Attr{slog.String("key", "val")})
	if _, ok := h2.(*ContextHandler); !ok {
		t.Errorf("WithAttrs returned %T, want *ContextHandler", h2)
	}
}

func TestContextHandler_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{})
	handler := NewContextHandler(base)

	h2 := handler.WithGroup("grp")
	if _, ok := h2.(*ContextHandler); !ok {
		t.Errorf("WithGroup returned %T, want *ContextHandler", h2)
	}
}

func TestSimplifySource(t *testing.T) {
	replacer := SimplifySource()

	src := &slog.Source{
		File: "/home/user/go/src/github.com/jcambass/tailhopper/internal/web/middleware.go",
		Line: 42,
	}
	attr := slog.Attr{
		Key:   slog.SourceKey,
		Value: slog.AnyValue(src),
	}

	result := replacer(nil, attr)
	if result.Key != "caller" {
		t.Errorf("key = %q, want %q", result.Key, "caller")
	}
	if result.Value.String() != "internal/web/middleware.go:42" {
		t.Errorf("value = %q, want %q", result.Value.String(), "internal/web/middleware.go:42")
	}
}

func TestSimplifySource_NonSourceKey(t *testing.T) {
	replacer := SimplifySource()

	attr := slog.String("msg", "hello")
	result := replacer(nil, attr)
	if result.Key != "msg" {
		t.Errorf("non-source key should pass through, got %q", result.Key)
	}
}
