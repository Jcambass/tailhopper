package web

import (
	"testing"

	"github.com/jcambass/tailhopper/internal/sse"
)

func TestNewServer(t *testing.T) {
	broadcaster := sse.NewSSEBroadcaster()

	// We can't easily construct a full Registry here without file system deps,
	// but we can verify the server constructor doesn't panic with nil registry.
	// In practice the nil registry would cause panics on requests, but construction should be fine.
	srv := NewServer("127.0.0.1:0", nil, broadcaster)

	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	if srv.addr != "127.0.0.1:0" {
		t.Errorf("addr = %q, want %q", srv.addr, "127.0.0.1:0")
	}
}
