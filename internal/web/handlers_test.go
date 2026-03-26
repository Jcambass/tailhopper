package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jcambass/tailhopper/internal/registry"
	"github.com/jcambass/tailhopper/internal/sse"
	"github.com/jcambass/tailhopper/internal/tailscale"
)

func setupTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	reg, err := registry.NewRegistry(path, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg
}

// chiRequest creates a request with chi URL params set.
func chiRequest(method, path string, params map[string]string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	return r.WithContext(ctx)
}

// writeTestConfig writes a registry config file for handler tests.
func writeTestConfig(t *testing.T, path string, configs []registry.PersistedTailnet) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(configs); err != nil {
		t.Fatal(err)
	}
}

func TestTailnetStartHandler_InvalidID(t *testing.T) {
	reg := setupTestRegistry(t)
	handler := tailnetStartHandler(reg)

	w := httptest.NewRecorder()
	r := chiRequest("POST", "/tailnet/abc/start", map[string]string{"id": "abc"})
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestTailnetStartHandler_NotFound(t *testing.T) {
	reg := setupTestRegistry(t)
	handler := tailnetStartHandler(reg)

	w := httptest.NewRecorder()
	r := chiRequest("POST", "/tailnet/999/start", map[string]string{"id": "999"})
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestTailnetStartHandler_ConflictState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	configs := []registry.PersistedTailnet{
		{ID: 1, StateDir: filepath.Join(dir, "1"), SocksPort: 0, Hostname: "host", TerminalError: "broken"},
	}
	writeTestConfig(t, path, configs)
	reg, _ := registry.NewRegistry(path, nil)

	handler := tailnetStartHandler(reg)
	w := httptest.NewRecorder()
	r := chiRequest("POST", "/tailnet/1/start", map[string]string{"id": "1"})
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestTailnetStopHandler_InvalidID(t *testing.T) {
	reg := setupTestRegistry(t)
	handler := tailnetStopHandler(reg)

	w := httptest.NewRecorder()
	r := chiRequest("POST", "/tailnet/abc/stop", map[string]string{"id": "abc"})
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestTailnetStopHandler_NotFound(t *testing.T) {
	reg := setupTestRegistry(t)
	handler := tailnetStopHandler(reg)

	w := httptest.NewRecorder()
	r := chiRequest("POST", "/tailnet/999/stop", map[string]string{"id": "999"})
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestTailnetStopHandler_ConflictState(t *testing.T) {
	reg := setupTestRegistry(t)
	reg.Add("host")

	handler := tailnetStopHandler(reg)
	w := httptest.NewRecorder()
	r := chiRequest("POST", "/tailnet/1/stop", map[string]string{"id": "1"})
	handler.ServeHTTP(w, r)

	// Newly added tailnet is in StoppedState, can't be stopped
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestTailnetDeleteHandler_InvalidID(t *testing.T) {
	reg := setupTestRegistry(t)
	broadcaster := sse.NewSSEBroadcaster()
	handler := tailnetDeleteHandler(reg, broadcaster)

	w := httptest.NewRecorder()
	r := chiRequest("DELETE", "/tailnet/abc", map[string]string{"id": "abc"})
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestTailnetDeleteHandler_NotFound(t *testing.T) {
	reg := setupTestRegistry(t)
	broadcaster := sse.NewSSEBroadcaster()
	handler := tailnetDeleteHandler(reg, broadcaster)

	w := httptest.NewRecorder()
	r := chiRequest("DELETE", "/tailnet/999", map[string]string{"id": "999"})
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestTailnetDeleteHandler_TerminalError_SkipsLogout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	configs := []registry.PersistedTailnet{
		{ID: 1, StateDir: filepath.Join(dir, "1"), SocksPort: 0, Hostname: "host", TerminalError: "broken"},
	}
	writeTestConfig(t, path, configs)

	broadcaster := sse.NewSSEBroadcaster()
	reg, _ := registry.NewRegistry(path, broadcaster)
	handler := tailnetDeleteHandler(reg, broadcaster)

	w := httptest.NewRecorder()
	r := chiRequest("DELETE", "/tailnet/1", map[string]string{"id": "1"})
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	_, ok := reg.Get(1)
	if ok {
		t.Error("expected tailnet to be deleted")
	}
}

func TestAddTailnetHandler_Success(t *testing.T) {
	reg := setupTestRegistry(t)
	broadcaster := sse.NewSSEBroadcaster()
	handler := addTailnetHandler(reg, broadcaster)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/tailnet/add", nil)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	list := reg.List()
	if len(list) != 1 {
		t.Errorf("expected 1 tailnet, got %d", len(list))
	}
}

func TestAddTailnetHandler_BlockedByUnconfigured(t *testing.T) {
	reg := setupTestRegistry(t)
	reg.Add("host1") // first tailnet is unconfigured (no magic DNS suffix)

	broadcaster := sse.NewSSEBroadcaster()
	handler := addTailnetHandler(reg, broadcaster)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/tailnet/add", nil)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}

	if len(reg.List()) != 1 {
		t.Errorf("expected 1 tailnet, got %d", len(reg.List()))
	}
}

func TestAddTailnetHandler_WrongMethod(t *testing.T) {
	reg := setupTestRegistry(t)
	broadcaster := sse.NewSSEBroadcaster()
	handler := addTailnetHandler(reg, broadcaster)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/tailnet/add", nil)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestDeleteResultMessage(t *testing.T) {
	tests := []struct {
		name     string
		state    tailscale.State
		err      error
		wantType string
	}{
		{"terminal error", tailscale.HasTerminalErrorState, nil, "success"},
		{"successful logout", tailscale.ConnectedState, nil, "success"},
		{"failed logout", tailscale.ConnectedState, http.ErrAbortHandler, "warning"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, toastType := deleteResultMessage(tt.state, tt.err)
			if msg == "" {
				t.Error("expected non-empty message")
			}
			if toastType != tt.wantType {
				t.Errorf("toast type = %q, want %q", toastType, tt.wantType)
			}
		})
	}
}

func TestLogoutBeforeDelete_TerminalError_Skips(t *testing.T) {
	err := logoutBeforeDelete(t.Context(), nil, 1, tailscale.HasTerminalErrorState)
	if err != nil {
		t.Errorf("expected nil error for terminal error state, got: %v", err)
	}
}

func TestServer_RoutesViaHTTP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	reg, _ := registry.NewRegistry(path, nil)
	broadcaster := sse.NewSSEBroadcaster()
	srv := NewServer("127.0.0.1:0", reg, broadcaster)

	// Test via the server's HTTP handler
	handler := srv.server.Handler

	t.Run("GET / returns 200", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("GET /proxy.pac returns 200", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/proxy.pac", nil)
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("GET /nonexistent returns 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/nonexistent", nil)
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	t.Run("POST /api/noop returns 200", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/noop", nil)
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("GET /ui/ redirects to /", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/ui/", nil)
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusTemporaryRedirect {
			t.Errorf("status = %d, want %d", w.Code, http.StatusTemporaryRedirect)
		}
		loc := w.Header().Get("Location")
		if loc != "/" {
			t.Errorf("redirect location = %q, want /", loc)
		}
	})
}
