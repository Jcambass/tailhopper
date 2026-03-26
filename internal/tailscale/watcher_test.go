package tailscale

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	tsnetpkg "github.com/jcambass/tailhopper/internal/tsnet"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
)

func TestIPNState_Refresh_State(t *testing.T) {
	state := ipn.Running
	n := &ipn.Notify{State: &state}

	s := IPNState{}
	s = s.refresh(n)

	if s.State == nil || *s.State != ipn.Running {
		t.Fatalf("expected Running, got %v", s.State)
	}
}

func TestIPNState_Refresh_ErrMessage(t *testing.T) {
	msg := "something went wrong"
	n := &ipn.Notify{ErrMessage: &msg}

	s := IPNState{}
	s = s.refresh(n)

	if s.ErrMessage == nil || *s.ErrMessage != msg {
		t.Fatalf("expected %q, got %v", msg, s.ErrMessage)
	}
}

func TestIPNState_Refresh_BrowseToURL(t *testing.T) {
	url := "https://login.tailscale.com/..."
	n := &ipn.Notify{BrowseToURL: &url}

	s := IPNState{}
	s = s.refresh(n)

	if s.BrowseToURL == nil || *s.BrowseToURL != url {
		t.Fatalf("expected %q, got %v", url, s.BrowseToURL)
	}
}

func TestIPNState_Refresh_PreservesOldState(t *testing.T) {
	// First notification sets state
	running := ipn.Running
	s := IPNState{}
	s = s.refresh(&ipn.Notify{State: &running})

	// Second notification sets err message but should preserve state
	msg := "error"
	s = s.refresh(&ipn.Notify{ErrMessage: &msg})

	if s.State == nil || *s.State != ipn.Running {
		t.Fatalf("state should be preserved, got %v", s.State)
	}
	if s.ErrMessage == nil || *s.ErrMessage != msg {
		t.Fatalf("err message should be set, got %v", s.ErrMessage)
	}
}

func TestIPNState_Refresh_OverwritesState(t *testing.T) {
	running := ipn.Running
	s := IPNState{}
	s = s.refresh(&ipn.Notify{State: &running})

	needsLogin := ipn.NeedsLogin
	s = s.refresh(&ipn.Notify{State: &needsLogin})

	if *s.State != ipn.NeedsLogin {
		t.Fatalf("expected NeedsLogin, got %v", *s.State)
	}
}

func TestIPNState_String_Empty(t *testing.T) {
	s := IPNState{}
	got := s.String()
	if got != "IPNState{}" {
		t.Fatalf("expected empty string representation, got %q", got)
	}
}

func TestIPNState_String_WithState(t *testing.T) {
	running := ipn.Running
	s := IPNState{State: &running}
	got := s.String()
	if got == "IPNState{}" {
		t.Fatal("expected non-empty string representation")
	}
}

func TestIPNState_String_WithPeers(t *testing.T) {
	s := IPNState{
		Peers: []tailcfg.NodeView{{}, {}},
	}
	got := s.String()
	if got == "IPNState{}" {
		t.Fatal("expected non-empty string representation with peers")
	}
}

func TestExtractMagicDNSSuffix(t *testing.T) {
	tests := []struct {
		name string
		fqdn string
		want string
	}{
		{"with trailing dot", "host.tail-scale.ts.net.", "tail-scale.ts.net"},
		{"without trailing dot", "host.tail-scale.ts.net", "tail-scale.ts.net"},
		{"single label with dot", "host.", ""},
		{"single label", "host", ""},
		{"empty", "", ""},
		{"two labels", "host.example.com", "example.com"},
		{"many labels", "host.a.b.c.d", "a.b.c.d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractMagicDNSSuffix(tt.fqdn)
			if got != tt.want {
				t.Errorf("extractMagicDNSSuffix(%q) = %q, want %q", tt.fqdn, got, tt.want)
			}
		})
	}
}

func TestParseLogKeyValues(t *testing.T) {
	tests := []struct {
		name      string
		msg       string
		wantAttrs int
		wantClean string
	}{
		{
			name:      "plain message",
			msg:       "hello world",
			wantAttrs: 0,
			wantClean: "hello world",
		},
		{
			name:      "key=value pairs",
			msg:       "starting server host=localhost port=8080",
			wantAttrs: 2,
			wantClean: "starting server",
		},
		{
			name:      "only key=value",
			msg:       "host=localhost",
			wantAttrs: 1,
			wantClean: "",
		},
		{
			name:      "url-like value parsed as key=value",
			msg:       "url=http://example.com",
			wantAttrs: 1,
			wantClean: "",
		},
		{
			name:      "invalid key with special chars",
			msg:       "foo/bar=baz",
			wantAttrs: 0,
			wantClean: "foo/bar=baz",
		},
		{
			name:      "empty value",
			msg:       "key=",
			wantAttrs: 1,
			wantClean: "",
		},
		{
			name:      "empty string",
			msg:       "",
			wantAttrs: 0,
			wantClean: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs, clean := parseLogKeyValues(tt.msg)
			if len(attrs) != tt.wantAttrs {
				t.Errorf("got %d attrs, want %d", len(attrs), tt.wantAttrs)
			}
			if clean != tt.wantClean {
				t.Errorf("got clean %q, want %q", clean, tt.wantClean)
			}
		})
	}
}

func TestNewWatcher_NilClient(t *testing.T) {
	_, err := NewWatcher(nil, func(context.Context, IPNState) {}, 1)
	if err == nil {
		t.Fatal("expected error with nil local client")
	}
}

func TestWatcher_DeliversNotifications(t *testing.T) {
	notifications := make(chan ipn.Notify, 3)
	running := ipn.Running
	notifications <- ipn.Notify{State: &running}

	var mu sync.Mutex
	var receivedStates []IPNState

	client := &tsnetpkg.MockLocalClient{
		WatchIPNBusFunc: func(ctx context.Context, mask ipn.NotifyWatchOpt) (tsnetpkg.IPNBusWatcher, error) {
			return &tsnetpkg.MockIPNBusWatcher{
				NextFunc: func() (ipn.Notify, error) {
					select {
					case n := <-notifications:
						return n, nil
					case <-ctx.Done():
						return ipn.Notify{}, ctx.Err()
					}
				},
			}, nil
		},
	}

	w, err := NewWatcher(client, func(ctx context.Context, state IPNState) {
		mu.Lock()
		receivedStates = append(receivedStates, state)
		mu.Unlock()
	}, 1)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	// Wait for the notification to be processed
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		count := len(receivedStates)
		mu.Unlock()
		if count >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for notification")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	w.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(receivedStates) < 1 {
		t.Fatal("expected at least 1 state callback")
	}
	if receivedStates[0].State == nil || *receivedStates[0].State != ipn.Running {
		t.Errorf("expected Running state, got %v", receivedStates[0].State)
	}
}

func TestWatcher_Close_StopsGoroutine(t *testing.T) {
	client := &tsnetpkg.MockLocalClient{
		WatchIPNBusFunc: func(ctx context.Context, mask ipn.NotifyWatchOpt) (tsnetpkg.IPNBusWatcher, error) {
			return &tsnetpkg.MockIPNBusWatcher{
				NextFunc: func() (ipn.Notify, error) {
					<-ctx.Done()
					return ipn.Notify{}, ctx.Err()
				},
			}, nil
		},
	}

	w, err := NewWatcher(client, func(context.Context, IPNState) {}, 1)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	// Close should not hang
	done := make(chan struct{})
	go func() {
		w.Close()
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(3 * time.Second):
		t.Fatal("Close() hung")
	}
}

func TestWatcher_WatchIPNBusError_Exits(t *testing.T) {
	client := &tsnetpkg.MockLocalClient{
		WatchIPNBusFunc: func(ctx context.Context, mask ipn.NotifyWatchOpt) (tsnetpkg.IPNBusWatcher, error) {
			return nil, fmt.Errorf("bus unavailable")
		},
	}

	w, err := NewWatcher(client, func(context.Context, IPNState) {}, 1)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	// Goroutine should exit quickly after error
	done := make(chan struct{})
	go func() {
		w.Close()
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(3 * time.Second):
		t.Fatal("Close() hung after WatchIPNBus error")
	}
}

func TestWatcher_NextError_Exits(t *testing.T) {
	callCount := 0
	client := &tsnetpkg.MockLocalClient{
		WatchIPNBusFunc: func(ctx context.Context, mask ipn.NotifyWatchOpt) (tsnetpkg.IPNBusWatcher, error) {
			return &tsnetpkg.MockIPNBusWatcher{
				NextFunc: func() (ipn.Notify, error) {
					callCount++
					if callCount == 1 {
						return ipn.Notify{}, fmt.Errorf("stream broken")
					}
					// Should never get here since the goroutine exits on error
					<-ctx.Done()
					return ipn.Notify{}, ctx.Err()
				},
			}, nil
		},
	}

	w, err := NewWatcher(client, func(context.Context, IPNState) {}, 1)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	// Goroutine should exit quickly after Next() error
	done := make(chan struct{})
	go func() {
		w.Close()
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(3 * time.Second):
		t.Fatal("Close() hung after Next() error")
	}
}

func TestWatcher_AccumulatesState(t *testing.T) {
	notifications := make(chan ipn.Notify, 10)

	// Send two notifications: first sets state, second sets err message
	running := ipn.Running
	notifications <- ipn.Notify{State: &running}
	errMsg := "warning"
	notifications <- ipn.Notify{ErrMessage: &errMsg}

	var mu sync.Mutex
	var receivedStates []IPNState

	client := &tsnetpkg.MockLocalClient{
		WatchIPNBusFunc: func(ctx context.Context, mask ipn.NotifyWatchOpt) (tsnetpkg.IPNBusWatcher, error) {
			return &tsnetpkg.MockIPNBusWatcher{
				NextFunc: func() (ipn.Notify, error) {
					select {
					case n := <-notifications:
						return n, nil
					case <-ctx.Done():
						return ipn.Notify{}, ctx.Err()
					}
				},
			}, nil
		},
	}

	w, err := NewWatcher(client, func(ctx context.Context, state IPNState) {
		mu.Lock()
		receivedStates = append(receivedStates, state)
		mu.Unlock()
	}, 1)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		count := len(receivedStates)
		mu.Unlock()
		if count >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for 2 notifications")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	w.Close()

	mu.Lock()
	defer mu.Unlock()

	// Second callback should have accumulated state from both notifications
	last := receivedStates[1]
	if last.State == nil || *last.State != ipn.Running {
		t.Errorf("expected Running state preserved, got %v", last.State)
	}
	if last.ErrMessage == nil || *last.ErrMessage != "warning" {
		t.Errorf("expected ErrMessage 'warning', got %v", last.ErrMessage)
	}
}
