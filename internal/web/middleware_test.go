package web

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jcambass/tailhopper/internal/logging"
)

type testLog struct {
	RequestID  string `json:"request_id"`
	RemoteAddr string `json:"remote_addr"`
	Error      string `json:"error"`
	Stack      string `json:"stack"`
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(logging.NewContextHandler(slog.NewJSONHandler(&buf, nil))))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func readLogs(t *testing.T, buf *bytes.Buffer) []testLog {
	t.Helper()
	var records []testLog
	decoder := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for decoder.More() {
		var record testLog
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func TestServerRequestLoggingContext(t *testing.T) {
	buf := captureLogs(t)
	server, _ := newTestServer(t)
	for _, suppliedID := range []string{"", "supplied-request-id"} {
		buf.Reset()
		req := httptest.NewRequest(http.MethodPost, "/api/noop", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("X-Request-ID", suppliedID)
		req.Header.Set("X-Real-IP", "203.0.113.1")
		req.Header.Set("X-Forwarded-For", "203.0.113.2")
		req.Header.Set("True-Client-IP", "203.0.113.3")
		rec := httptest.NewRecorder()
		server.server.Handler.ServeHTTP(rec, req)

		records := readLogs(t, buf)
		if len(records) != 2 {
			t.Fatalf("unexpected request/response logs: %+v", records)
		}
		id := records[0].RequestID
		if id == "" {
			t.Fatal("request ID is missing")
		}
		if suppliedID != "" && id != suppliedID {
			t.Errorf("request ID = %q, want supplied ID %q", id, suppliedID)
		}
		if records[1].RequestID != id {
			t.Errorf("response request ID = %q, want %q", records[1].RequestID, id)
		}
		if records[0].RemoteAddr != req.RemoteAddr {
			t.Errorf("logged peer = %q, want %q", records[0].RemoteAddr, req.RemoteAddr)
		}
	}
}

func TestPanicRecovery(t *testing.T) {
	buf := captureLogs(t)
	handler := withLoggingContext(panicRecoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("handler failed")
	})))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "panic-request")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError || rec.Body.Len() != 0 {
		t.Fatalf("panic response = %d %q, want an empty 500", rec.Code, rec.Body.String())
	}
	records := readLogs(t, buf)
	if len(records) != 1 {
		t.Fatalf("unexpected panic logs: %+v", records)
	}
	if records[0].RequestID != "panic-request" || records[0].Error != "handler failed" || records[0].Stack == "" {
		t.Errorf("panic log is missing request context or details: %+v", records[0])
	}
}

func TestPanicRecoveryPreservesAbortHandler(t *testing.T) {
	buf := captureLogs(t)
	handler := panicRecoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Errorf("recovered panic = %v, want http.ErrAbortHandler", got)
		}
		if buf.Len() != 0 {
			t.Errorf("http.ErrAbortHandler should not be logged: %s", buf.String())
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}
