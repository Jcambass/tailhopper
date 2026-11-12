package web

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jcambass/tailhopper/internal/registry"
	"github.com/jcambass/tailhopper/internal/sse"
)

func newTestServer(t *testing.T) (*Server, *sse.SSEBroadcaster) {
	t.Helper()
	broadcaster := sse.NewSSEBroadcaster()
	reg, err := registry.NewRegistry(filepath.Join(t.TempDir(), "tailhopper.json"), broadcaster)
	if err != nil {
		t.Fatal(err)
	}
	return NewServer("127.0.0.1:8888", reg, broadcaster), broadcaster
}

func TestServerRoutes(t *testing.T) {
	server, _ := newTestServer(t)
	tests := []struct {
		method   string
		path     string
		status   int
		body     string
		location string
	}{
		{method: http.MethodGet, path: "/", status: http.StatusOK},
		{method: http.MethodHead, path: "/", status: http.StatusOK},
		{method: http.MethodGet, path: "/missing", status: http.StatusNotFound},
		{method: http.MethodGet, path: "/proxy.pac", status: http.StatusOK, body: "FindProxyForURL"},
		{method: http.MethodGet, path: "/static/favicon.svg", status: http.StatusOK, body: "<svg"},
		{method: http.MethodGet, path: "/ui/", status: http.StatusTemporaryRedirect, location: "/"},
		{method: http.MethodGet, path: "/ui/missing", status: http.StatusNotFound},
		{method: http.MethodPost, path: "/api/noop", status: http.StatusOK},
		{method: http.MethodGet, path: "/api/noop", status: http.StatusMethodNotAllowed},
		{method: http.MethodPost, path: "/tailnet/7/start", status: http.StatusNotFound, body: "tailnet not found"},
		{method: http.MethodPost, path: "/tailnet/7/stop", status: http.StatusNotFound, body: "tailnet not found"},
		{method: http.MethodDelete, path: "/tailnet/7", status: http.StatusNotFound, body: "tailnet not found"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body = %q", rec.Code, tt.status, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.body) {
				t.Errorf("body = %q, want it to contain %q", rec.Body.String(), tt.body)
			}
			if got := rec.Header().Get("Location"); got != tt.location {
				t.Errorf("Location = %q, want %q", got, tt.location)
			}
		})
	}
}

func TestServerHeadEventsDoesNotStream(t *testing.T) {
	server, _ := newTestServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodHead, "/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(rec, req)

	if ctx.Err() != nil {
		t.Fatal("HEAD waited for request cancellation instead of returning")
	}
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Flushed {
		t.Fatalf("HEAD must not stream: status=%d, body=%q, flushed=%v", rec.Code, rec.Body.String(), rec.Flushed)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("HEAD Content-Type = %q, want text/event-stream", got)
	}
}

func TestServerStreamsEvents(t *testing.T) {
	server, broadcaster := newTestServer(t)
	ts := httptest.NewServer(server.server.Handler)
	defer ts.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("SSE headers were not flushed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("unexpected SSE response: %s, %v", resp.Status, resp.Header)
	}

	reader := bufio.NewReader(resp.Body)
	readEvent := func(want string) {
		t.Helper()
		for _, line := range []string{"event: " + want + "\n", "data: update\n", "\n"} {
			got, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("SSE event was not flushed: %v", err)
			}
			if got != line {
				t.Fatalf("SSE line = %q, want %q", got, line)
			}
		}
	}

	readEvent("global")
	broadcaster.BroadcastTailnetChange(7)
	readEvent("tailnet-7")
}
