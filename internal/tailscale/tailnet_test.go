package tailscale

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"

	tsnetpkg "github.com/jcambass/tailhopper/internal/tsnet"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
)

// mockObserver records OnChange calls for assertions.
type mockObserver struct {
	mu        sync.Mutex
	snapshots []TailnetSnapshot
}

func newMockObserver() *mockObserver {
	return &mockObserver{}
}

func (m *mockObserver) OnChange(snapshot TailnetSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshots = append(m.snapshots, snapshot)
}

func (m *mockObserver) lastSnapshot() (TailnetSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.snapshots) == 0 {
		return TailnetSnapshot{}, false
	}
	return m.snapshots[len(m.snapshots)-1], true
}

func (m *mockObserver) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.snapshots)
}

// --- NewTailnet ---

func TestNewTailnet_Defaults(t *testing.T) {
	tn := NewTailnet(1, "/tmp/state", "my-host", "", "", false, 1080, nil, nil)

	if tn.ID() != 1 {
		t.Errorf("ID() = %d, want 1", tn.ID())
	}
	if tn.SocksAddr() != "localhost:1080" {
		t.Errorf("SocksAddr() = %q, want %q", tn.SocksAddr(), "localhost:1080")
	}

	snap := tn.Snapshot()
	if snap.State != StoppedState {
		t.Errorf("initial state = %q, want %q", snap.State, StoppedState)
	}
	if snap.UserState != UserDisabled {
		t.Errorf("initial user state = %q, want %q", snap.UserState, UserDisabled)
	}
	if snap.Hostname != "my-host" {
		t.Errorf("hostname = %q, want %q", snap.Hostname, "my-host")
	}
	if snap.MagicDNSSuffix != "" {
		t.Errorf("magic DNS suffix = %q, want empty", snap.MagicDNSSuffix)
	}
	if snap.TerminalError != "" {
		t.Errorf("terminal error = %q, want empty", snap.TerminalError)
	}
}

func TestNewTailnet_UserEnabled(t *testing.T) {
	tn := NewTailnet(2, "/tmp/state", "host", "", "", true, 1081, nil, nil)

	snap := tn.Snapshot()
	if snap.UserState != UserEnabled {
		t.Errorf("user state = %q, want %q", snap.UserState, UserEnabled)
	}
	if snap.State != StoppedState {
		t.Errorf("state = %q, want %q", snap.State, StoppedState)
	}
}

func TestNewTailnet_WithTerminalError(t *testing.T) {
	tn := NewTailnet(3, "/tmp/state", "host", "", "fatal error", true, 1082, nil, nil)

	snap := tn.Snapshot()
	if snap.State != HasTerminalErrorState {
		t.Errorf("state = %q, want %q", snap.State, HasTerminalErrorState)
	}
	if snap.UserState != UserDisabled {
		t.Errorf("user state = %q, want %q (terminal errors force disabled)", snap.UserState, UserDisabled)
	}
	if snap.TerminalError != "fatal error" {
		t.Errorf("terminal error = %q, want %q", snap.TerminalError, "fatal error")
	}
}

func TestNewTailnet_WithClaimedSuffix(t *testing.T) {
	tn := NewTailnet(4, "/tmp/state", "host", "my-tailnet.ts.net", "", false, 1083, nil, nil)

	snap := tn.Snapshot()
	if snap.MagicDNSSuffix != "my-tailnet.ts.net" {
		t.Errorf("magic DNS suffix = %q, want %q", snap.MagicDNSSuffix, "my-tailnet.ts.net")
	}
}

func TestTailnetSnapshot_String(t *testing.T) {
	snap := &TailnetSnapshot{
		ID:        1,
		State:     ConnectedState,
		UserState: UserEnabled,
		Hostname:  "test-host",
	}

	s := snap.String()
	if s == "" {
		t.Fatal("expected non-empty string")
	}
}

func TestTailnet_SocksAddr(t *testing.T) {
	tests := []struct {
		port int
		want string
	}{
		{1080, "localhost:1080"},
		{0, "localhost:0"},
		{65535, "localhost:65535"},
	}

	for _, tt := range tests {
		tn := NewTailnet(1, "/tmp", "host", "", "", false, tt.port, nil, nil)
		if tn.SocksAddr() != tt.want {
			t.Errorf("SocksAddr() with port %d = %q, want %q", tt.port, tn.SocksAddr(), tt.want)
		}
	}
}

// --- IPN transition helpers ---

func TestTailnet_MaybeTransitionToNeedsLoginLocked(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)

	t.Run("no state", func(t *testing.T) {
		if tn.maybeTransitionToNeedsLoginLocked(IPNState{}) {
			t.Error("expected false with no state")
		}
	})

	t.Run("wrong state", func(t *testing.T) {
		running := ipn.Running
		if tn.maybeTransitionToNeedsLoginLocked(IPNState{State: &running}) {
			t.Error("expected false for Running state")
		}
	})

	t.Run("needs login but no URL", func(t *testing.T) {
		needsLogin := ipn.NeedsLogin
		if tn.maybeTransitionToNeedsLoginLocked(IPNState{State: &needsLogin}) {
			t.Error("expected false without BrowseToURL")
		}
	})

	t.Run("needs login with empty URL", func(t *testing.T) {
		needsLogin := ipn.NeedsLogin
		empty := ""
		if tn.maybeTransitionToNeedsLoginLocked(IPNState{State: &needsLogin, BrowseToURL: &empty}) {
			t.Error("expected false with empty BrowseToURL")
		}
	})

	t.Run("needs login with URL", func(t *testing.T) {
		needsLogin := ipn.NeedsLogin
		url := "https://login.tailscale.com/abc"
		if !tn.maybeTransitionToNeedsLoginLocked(IPNState{State: &needsLogin, BrowseToURL: &url}) {
			t.Error("expected true for NeedsLogin with URL")
		}
		if tn.currentState != NeedsLoginState {
			t.Errorf("state = %q, want %q", tn.currentState, NeedsLoginState)
		}
		if tn.loginURL != url {
			t.Errorf("loginURL = %q, want %q", tn.loginURL, url)
		}
	})
}

func TestTailnet_MaybeTransitionToNeedsMachineAuthLocked(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)

	t.Run("no state", func(t *testing.T) {
		if tn.maybeTransitionToNeedsMachineAuthLocked(IPNState{}) {
			t.Error("expected false with no state")
		}
	})

	t.Run("wrong state", func(t *testing.T) {
		running := ipn.Running
		if tn.maybeTransitionToNeedsMachineAuthLocked(IPNState{State: &running}) {
			t.Error("expected false for Running state")
		}
	})

	t.Run("needs machine auth", func(t *testing.T) {
		needsMachineAuth := ipn.NeedsMachineAuth
		if !tn.maybeTransitionToNeedsMachineAuthLocked(IPNState{State: &needsMachineAuth}) {
			t.Error("expected true for NeedsMachineAuth state")
		}
		if tn.currentState != NeedsMachineAuthState {
			t.Errorf("state = %q, want %q", tn.currentState, NeedsMachineAuthState)
		}
	})
}

func TestTailnet_MaybeTransitionToConnectedLocked(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)

	t.Run("no state", func(t *testing.T) {
		if tn.maybeTransitionToConnectedLocked(IPNState{}) {
			t.Error("expected false with no state")
		}
	})

	t.Run("wrong state", func(t *testing.T) {
		needsLogin := ipn.NeedsLogin
		if tn.maybeTransitionToConnectedLocked(IPNState{State: &needsLogin}) {
			t.Error("expected false for NeedsLogin state")
		}
	})

	t.Run("running", func(t *testing.T) {
		running := ipn.Running
		if !tn.maybeTransitionToConnectedLocked(IPNState{State: &running}) {
			t.Error("expected true for Running state")
		}
		if tn.currentState != ConnectedState {
			t.Errorf("state = %q, want %q", tn.currentState, ConnectedState)
		}
	})
}

func TestTailnet_UpdatePeersLocked(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)

	snap := tn.Snapshot()
	if len(snap.Peers) != 0 {
		t.Errorf("expected 0 peers, got %d", len(snap.Peers))
	}

	if !tn.updatePeersLocked(IPNState{}) {
		t.Error("expected true from updatePeersLocked")
	}
}

func TestTailnet_MaybeClaimMagicDNSSuffixLocked(t *testing.T) {
	t.Run("no suffix in IPN state", func(t *testing.T) {
		tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)
		if tn.maybeClaimMagicDNSSuffixLocked(IPNState{}) {
			t.Error("expected false with empty suffix")
		}
	})

	t.Run("already claimed with same suffix", func(t *testing.T) {
		tn := NewTailnet(2, "/tmp", "host", "existing.ts.net", "", false, 1080, nil, nil)
		if tn.maybeClaimMagicDNSSuffixLocked(IPNState{MagicDNSSuffix: "existing.ts.net"}) {
			t.Error("expected false when already claimed with same suffix")
		}
	})

	t.Run("successful claim", func(t *testing.T) {
		tn := NewTailnet(3, "/tmp", "host", "", "", false, 1080, nil, nil)
		if !tn.maybeClaimMagicDNSSuffixLocked(IPNState{MagicDNSSuffix: "new-tailnet.ts.net"}) {
			t.Error("expected true on successful claim")
		}
		if tn.claimedMagicDNSSuffix != "new-tailnet.ts.net" {
			t.Errorf("claimedMagicDNSSuffix = %q, want %q", tn.claimedMagicDNSSuffix, "new-tailnet.ts.net")
		}
	})

	t.Run("suffix mismatch with existing claim", func(t *testing.T) {
		tn := NewTailnet(5, "/tmp", "host", "original.ts.net", "", false, 1080, nil, nil)
		if tn.maybeClaimMagicDNSSuffixLocked(IPNState{MagicDNSSuffix: "different.ts.net"}) {
			t.Error("expected false when suffix mismatches (just logs error)")
		}
	})
}

func TestTailnet_MaybeUpdateSelfNodeHostnameLocked(t *testing.T) {
	t.Run("invalid self node", func(t *testing.T) {
		tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)
		if tn.maybeUpdateSelfNodeHostnameLocked(IPNState{}) {
			t.Error("expected false with invalid self node")
		}
	})

	t.Run("updates hostname", func(t *testing.T) {
		tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)
		selfNode := (&tailcfg.Node{ComputedName: "actual-host"}).View()
		if !tn.maybeUpdateSelfNodeHostnameLocked(IPNState{SelfNode: selfNode}) {
			t.Error("expected true when updating hostname")
		}
		if tn.selfNodeHostname != "actual-host" {
			t.Errorf("selfNodeHostname = %q, want %q", tn.selfNodeHostname, "actual-host")
		}
	})

	t.Run("same hostname returns false", func(t *testing.T) {
		tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)
		tn.selfNodeHostname = "actual-host"
		selfNode := (&tailcfg.Node{ComputedName: "actual-host"}).View()
		if tn.maybeUpdateSelfNodeHostnameLocked(IPNState{SelfNode: selfNode}) {
			t.Error("expected false when hostname unchanged")
		}
	})
}

// --- Observer ---

func TestTailnet_NoopObserver(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)
	// Calling OnChange on noopObserver should not panic.
	tn.observer.OnChange(TailnetSnapshot{})
}

func TestTailnet_ObserverCalledOnStateChange(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	// setState calls observer.OnChange
	tn.mu.Lock()
	tn.setState(ConnectedState)
	tn.mu.Unlock()

	if obs.callCount() < 1 {
		t.Error("expected observer to be called on setState")
	}
}

// --- SetTerminalError ---

func TestTailnet_SetTerminalError(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", true, 1080, obs, nil)

	tn.SetTerminalError("fatal: something bad")

	snap := tn.Snapshot()
	if snap.State != HasTerminalErrorState {
		t.Errorf("state = %q, want %q", snap.State, HasTerminalErrorState)
	}
	if snap.UserState != UserDisabled {
		t.Errorf("user state = %q, want %q", snap.UserState, UserDisabled)
	}
	if snap.TerminalError != "fatal: something bad" {
		t.Errorf("terminal error = %q, want %q", snap.TerminalError, "fatal: something bad")
	}

	last, ok := obs.lastSnapshot()
	if !ok {
		t.Fatal("expected observer to be called")
	}
	if last.State != HasTerminalErrorState {
		t.Errorf("observer snapshot state = %q, want %q", last.State, HasTerminalErrorState)
	}
}

// --- State guards ---

func TestTailnet_StartRequiresStoppedState(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	err := tn.Start(context.Background())
	if err == nil {
		t.Fatal("expected error when starting from non-stopped state")
	}
}

func TestTailnet_StopRequiresValidState(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)

	err := tn.Stop(context.Background())
	if err == nil {
		t.Fatal("expected error when stopping from StoppedState")
	}
}

func TestTailnet_DialRequiresConnectedState(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)

	_, err := tn.Dial(context.Background(), "tcp", "example.com:80")
	if err == nil {
		t.Fatal("expected error when dialing from StoppedState")
	}
}

// --- Start / Stop / Logout lifecycle ---

func newStartableTailnet(t *testing.T, obs TailnetObserver) (*Tailnet, *tsnetpkg.MockTSNetServer) {
	t.Helper()
	mockServer := tsnetpkg.NewMockTSNetServer()

	mockServer.LocalClientFunc = func() (tsnetpkg.LocalClient, error) {
		return &tsnetpkg.MockLocalClient{
			WatchIPNBusFunc: func(ctx context.Context, mask ipn.NotifyWatchOpt) (tsnetpkg.IPNBusWatcher, error) {
				return &tsnetpkg.MockIPNBusWatcher{
					NextFunc: func() (ipn.Notify, error) {
						<-ctx.Done()
						return ipn.Notify{}, ctx.Err()
					},
				}, nil
			},
			LogoutFunc: func(ctx context.Context) error {
				return nil
			},
		}, nil
	}

	factory := func(config tsnetpkg.TSNetServerConfig) tsnetpkg.TSNetServer {
		return mockServer
	}
	tn := NewTailnet(1, t.TempDir(), "host", "", "", false, 0, obs, factory)
	return tn, mockServer
}

func TestTailnet_Start_Success(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)

	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(context.Background())

	snap := tn.Snapshot()
	if snap.State != StartedState {
		t.Errorf("state = %q, want %q", snap.State, StartedState)
	}
	if snap.UserState != UserEnabled {
		t.Errorf("user state = %q, want %q", snap.UserState, UserEnabled)
	}
}

func TestTailnet_Start_SocksProxyFailure_Rollback(t *testing.T) {
	obs := newMockObserver()
	mockServer := tsnetpkg.NewMockTSNetServer()
	factory := func(config tsnetpkg.TSNetServerConfig) tsnetpkg.TSNetServer {
		return mockServer
	}

	// Occupy a port so SOCKS proxy creation fails.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	tn := NewTailnet(1, t.TempDir(), "host", "", "", false, port, obs, factory)

	err = tn.Start(context.Background())
	if err == nil {
		t.Fatal("expected error when SOCKS proxy fails")
	}

	snap := tn.Snapshot()
	if snap.State != StoppedState {
		t.Errorf("state after failed start = %q, want %q", snap.State, StoppedState)
	}
}

func TestTailnet_Start_ServerStartFailure_Rollback(t *testing.T) {
	obs := newMockObserver()
	mockServer := tsnetpkg.NewMockTSNetServer()
	mockServer.StartFunc = func() error {
		return fmt.Errorf("server start failed")
	}
	factory := func(config tsnetpkg.TSNetServerConfig) tsnetpkg.TSNetServer {
		return mockServer
	}

	tn := NewTailnet(1, t.TempDir(), "host", "", "", false, 0, obs, factory)

	err := tn.Start(context.Background())
	if err == nil {
		t.Fatal("expected error when server start fails")
	}

	snap := tn.Snapshot()
	if snap.State != StoppedState {
		t.Errorf("state after failed start = %q, want %q", snap.State, StoppedState)
	}
}

func TestTailnet_Start_LocalClientFailure_Rollback(t *testing.T) {
	obs := newMockObserver()
	mockServer := tsnetpkg.NewMockTSNetServer()
	mockServer.LocalClientFunc = func() (tsnetpkg.LocalClient, error) {
		return nil, fmt.Errorf("local client error")
	}
	serverClosed := false
	mockServer.CloseFunc = func() error {
		serverClosed = true
		return nil
	}
	factory := func(config tsnetpkg.TSNetServerConfig) tsnetpkg.TSNetServer {
		return mockServer
	}

	tn := NewTailnet(1, t.TempDir(), "host", "", "", false, 0, obs, factory)

	err := tn.Start(context.Background())
	if err == nil {
		t.Fatal("expected error when LocalClient fails")
	}

	if !serverClosed {
		t.Error("expected server to be closed during rollback")
	}

	snap := tn.Snapshot()
	if snap.State != StoppedState {
		t.Errorf("state after failed start = %q, want %q", snap.State, StoppedState)
	}
}

func TestTailnet_Start_WatcherCreationFailure_Rollback(t *testing.T) {
	obs := newMockObserver()
	mockServer := tsnetpkg.NewMockTSNetServer()
	mockServer.LocalClientFunc = func() (tsnetpkg.LocalClient, error) {
		return nil, nil // nil client causes NewWatcher to fail
	}
	serverClosed := false
	mockServer.CloseFunc = func() error {
		serverClosed = true
		return nil
	}
	factory := func(config tsnetpkg.TSNetServerConfig) tsnetpkg.TSNetServer {
		return mockServer
	}

	tn := NewTailnet(1, t.TempDir(), "host", "", "", false, 0, obs, factory)

	err := tn.Start(context.Background())
	if err == nil {
		t.Fatal("expected error when watcher creation fails")
	}

	if !serverClosed {
		t.Error("expected server to be closed during rollback")
	}

	snap := tn.Snapshot()
	if snap.State != StoppedState {
		t.Errorf("state after failed start = %q, want %q", snap.State, StoppedState)
	}
}

func TestTailnet_Stop_FromConnected(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)

	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	if err := tn.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	snap := tn.Snapshot()
	if snap.State != StoppedState {
		t.Errorf("state = %q, want %q", snap.State, StoppedState)
	}
	if snap.UserState != UserDisabled {
		t.Errorf("user state = %q, want %q", snap.UserState, UserDisabled)
	}
}

func TestTailnet_Stop_FromStarted(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)

	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := tn.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	snap := tn.Snapshot()
	if snap.State != StoppedState {
		t.Errorf("state = %q, want %q", snap.State, StoppedState)
	}
}

func TestTailnet_Stop_FromNeedsLogin(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)

	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	tn.mu.Lock()
	tn.currentState = NeedsLoginState
	tn.mu.Unlock()

	if err := tn.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	snap := tn.Snapshot()
	if snap.State != StoppedState {
		t.Errorf("state = %q, want %q", snap.State, StoppedState)
	}
}

func TestTailnet_Stop_NilsOutComponents(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)

	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := tn.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	tn.mu.RLock()
	defer tn.mu.RUnlock()
	if tn.server != nil {
		t.Error("expected server to be nil after stop")
	}
	if tn.watcher != nil {
		t.Error("expected watcher to be nil after stop")
	}
	if tn.socksProxy != nil {
		t.Error("expected socksProxy to be nil after stop")
	}
}

func TestTailnet_Stop_InvalidStates(t *testing.T) {
	invalidStates := []State{StoppedState, HasTerminalErrorState, LoggingOutState}
	for _, state := range invalidStates {
		t.Run(string(state), func(t *testing.T) {
			tn := NewTailnet(1, "/tmp", "host", "", "", false, 0, nil, nil)
			tn.mu.Lock()
			tn.currentState = state
			tn.mu.Unlock()

			err := tn.Stop(context.Background())
			if err == nil {
				t.Errorf("expected error when stopping from %s", state)
			}
		})
	}
}

func TestTailnet_Logout_FromStopped(t *testing.T) {
	obs := newMockObserver()
	logoutCalled := false
	mockServer := tsnetpkg.NewMockTSNetServer()
	mockServer.LocalClientFunc = func() (tsnetpkg.LocalClient, error) {
		return &tsnetpkg.MockLocalClient{
			LogoutFunc: func(ctx context.Context) error {
				logoutCalled = true
				return nil
			},
		}, nil
	}
	factory := func(config tsnetpkg.TSNetServerConfig) tsnetpkg.TSNetServer {
		return mockServer
	}

	tn := NewTailnet(1, t.TempDir(), "host", "", "", false, 0, obs, factory)

	if err := tn.Logout(context.Background()); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	if !logoutCalled {
		t.Error("expected Logout to be called on LocalClient")
	}

	snap := tn.Snapshot()
	if snap.State != StoppedState {
		t.Errorf("state = %q, want %q", snap.State, StoppedState)
	}
	if snap.UserState != UserDisabled {
		t.Errorf("user state = %q, want %q", snap.UserState, UserDisabled)
	}
}

func TestTailnet_Logout_FromNeedsLogin_IsNoop(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)

	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	tn.mu.Lock()
	tn.currentState = NeedsLoginState
	tn.mu.Unlock()

	if err := tn.Logout(context.Background()); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	snap := tn.Snapshot()
	if snap.State != NeedsLoginState {
		t.Errorf("state = %q, want %q", snap.State, NeedsLoginState)
	}
}

func TestTailnet_Logout_FromConnected(t *testing.T) {
	obs := newMockObserver()
	logoutCalled := false
	mockServer := tsnetpkg.NewMockTSNetServer()
	mockServer.LocalClientFunc = func() (tsnetpkg.LocalClient, error) {
		return &tsnetpkg.MockLocalClient{
			LogoutFunc: func(ctx context.Context) error {
				logoutCalled = true
				return nil
			},
			WatchIPNBusFunc: func(ctx context.Context, mask ipn.NotifyWatchOpt) (tsnetpkg.IPNBusWatcher, error) {
				return &tsnetpkg.MockIPNBusWatcher{
					NextFunc: func() (ipn.Notify, error) {
						<-ctx.Done()
						return ipn.Notify{}, ctx.Err()
					},
				}, nil
			},
		}, nil
	}
	factory := func(config tsnetpkg.TSNetServerConfig) tsnetpkg.TSNetServer {
		return mockServer
	}

	tn := NewTailnet(1, t.TempDir(), "host", "", "", false, 0, obs, factory)
	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	if err := tn.Logout(context.Background()); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	if !logoutCalled {
		t.Error("expected Logout to be called on LocalClient")
	}

	snap := tn.Snapshot()
	if snap.State != StoppedState {
		t.Errorf("state = %q, want %q", snap.State, StoppedState)
	}
}

func TestTailnet_Logout_Error_StillTransitionsToStopped(t *testing.T) {
	obs := newMockObserver()
	mockServer := tsnetpkg.NewMockTSNetServer()
	mockServer.LocalClientFunc = func() (tsnetpkg.LocalClient, error) {
		return &tsnetpkg.MockLocalClient{
			LogoutFunc: func(ctx context.Context) error {
				return fmt.Errorf("logout network error")
			},
		}, nil
	}
	factory := func(config tsnetpkg.TSNetServerConfig) tsnetpkg.TSNetServer {
		return mockServer
	}

	tn := NewTailnet(1, t.TempDir(), "host", "", "", false, 0, obs, factory)

	err := tn.Logout(context.Background())
	if err == nil {
		t.Fatal("expected error from logout")
	}

	snap := tn.Snapshot()
	if snap.State != StoppedState {
		t.Errorf("state = %q, want %q (should transition to stopped even on error)", snap.State, StoppedState)
	}
}

func TestTailnet_Logout_InvalidStates(t *testing.T) {
	invalidStates := []State{HasTerminalErrorState, LoggingOutState}
	for _, state := range invalidStates {
		t.Run(string(state), func(t *testing.T) {
			tn := NewTailnet(1, "/tmp", "host", "", "", false, 0, nil, nil)
			tn.mu.Lock()
			tn.currentState = state
			tn.mu.Unlock()

			err := tn.Logout(context.Background())
			if err == nil {
				t.Errorf("expected error when logging out from %s", state)
			}
		})
	}
}

func TestTailnet_Logout_SetsLoggingOutState(t *testing.T) {
	obs := newMockObserver()
	stateObserved := make(chan State, 1)

	// Declare tn first so the closure below can reference it.
	var tn *Tailnet

	mockServer := tsnetpkg.NewMockTSNetServer()
	mockServer.LocalClientFunc = func() (tsnetpkg.LocalClient, error) {
		return &tsnetpkg.MockLocalClient{
			LogoutFunc: func(ctx context.Context) error {
				// Snapshot during the logout call (lock is released, so we can read)
				snap := tn.Snapshot()
				stateObserved <- snap.State
				return nil
			},
		}, nil
	}
	factory := func(config tsnetpkg.TSNetServerConfig) tsnetpkg.TSNetServer {
		return mockServer
	}

	tn = NewTailnet(1, t.TempDir(), "host", "", "", false, 0, obs, factory)

	if err := tn.Logout(context.Background()); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	state := <-stateObserved
	if state != LoggingOutState {
		t.Errorf("state during logout = %q, want %q", state, LoggingOutState)
	}
}

func TestTailnet_Dial_FromConnected(t *testing.T) {
	obs := newMockObserver()
	dialCalled := false
	mockServer := tsnetpkg.NewMockTSNetServer()
	mockServer.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialCalled = true
		if network != "tcp" {
			t.Errorf("network = %q, want tcp", network)
		}
		if addr != "example.com:80" {
			t.Errorf("addr = %q, want example.com:80", addr)
		}
		return nil, nil
	}
	mockServer.LocalClientFunc = func() (tsnetpkg.LocalClient, error) {
		return &tsnetpkg.MockLocalClient{
			WatchIPNBusFunc: func(ctx context.Context, mask ipn.NotifyWatchOpt) (tsnetpkg.IPNBusWatcher, error) {
				return &tsnetpkg.MockIPNBusWatcher{
					NextFunc: func() (ipn.Notify, error) {
						<-ctx.Done()
						return ipn.Notify{}, ctx.Err()
					},
				}, nil
			},
		}, nil
	}
	factory := func(config tsnetpkg.TSNetServerConfig) tsnetpkg.TSNetServer {
		return mockServer
	}

	tn := NewTailnet(1, t.TempDir(), "host", "", "", false, 0, obs, factory)
	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(context.Background())

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	_, err := tn.Dial(context.Background(), "tcp", "example.com:80")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if !dialCalled {
		t.Error("expected Dial to be called on server")
	}
}

func TestTailnet_Start_DoubleStart_Rejected(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)

	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer tn.Stop(context.Background())

	err := tn.Start(context.Background())
	if err == nil {
		t.Fatal("expected error on double start")
	}
}

func TestTailnet_StartStopStart_Cycle(t *testing.T) {
	obs := newMockObserver()

	mockServer := tsnetpkg.NewMockTSNetServer()
	mockServer.LocalClientFunc = func() (tsnetpkg.LocalClient, error) {
		return &tsnetpkg.MockLocalClient{
			WatchIPNBusFunc: func(ctx context.Context, mask ipn.NotifyWatchOpt) (tsnetpkg.IPNBusWatcher, error) {
				return &tsnetpkg.MockIPNBusWatcher{
					NextFunc: func() (ipn.Notify, error) {
						<-ctx.Done()
						return ipn.Notify{}, ctx.Err()
					},
				}, nil
			},
		}, nil
	}
	factory := func(config tsnetpkg.TSNetServerConfig) tsnetpkg.TSNetServer {
		m := tsnetpkg.NewMockTSNetServer()
		m.LocalClientFunc = mockServer.LocalClientFunc
		return m
	}

	tn := NewTailnet(1, t.TempDir(), "host", "", "", false, 0, obs, factory)

	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := tn.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop: %v", err)
	}

	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if err := tn.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}

	snap := tn.Snapshot()
	if snap.State != StoppedState {
		t.Errorf("final state = %q, want %q", snap.State, StoppedState)
	}
}

func TestTailnet_Snapshot_UsesSelfNodeHostname(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)

	snap := tn.Snapshot()
	if snap.Hostname != "host" {
		t.Errorf("hostname = %q, want %q (user-set)", snap.Hostname, "host")
	}

	tn.mu.Lock()
	tn.selfNodeHostname = "actual-host"
	tn.mu.Unlock()

	snap = tn.Snapshot()
	if snap.Hostname != "actual-host" {
		t.Errorf("hostname = %q, want %q (self-node)", snap.Hostname, "actual-host")
	}
}

// --- reactToIPNStateChange ---

func TestTailnet_ReactToIPN_StartedToConnected(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)
	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(context.Background())

	running := ipn.Running
	tn.reactToIPNStateChange(context.Background(), IPNState{State: &running})

	snap := tn.Snapshot()
	if snap.State != ConnectedState {
		t.Errorf("state = %q, want %q", snap.State, ConnectedState)
	}
}

func TestTailnet_ReactToIPN_StartedToNeedsLogin(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)
	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(context.Background())

	needsLogin := ipn.NeedsLogin
	loginURL := "https://login.tailscale.com/abc"
	tn.reactToIPNStateChange(context.Background(), IPNState{State: &needsLogin, BrowseToURL: &loginURL})

	snap := tn.Snapshot()
	if snap.State != NeedsLoginState {
		t.Errorf("state = %q, want %q", snap.State, NeedsLoginState)
	}
	if snap.LoginURL != loginURL {
		t.Errorf("loginURL = %q, want %q", snap.LoginURL, loginURL)
	}
}

func TestTailnet_ReactToIPN_StartedToNeedsMachineAuth(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)
	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(context.Background())

	needsMachineAuth := ipn.NeedsMachineAuth
	tn.reactToIPNStateChange(context.Background(), IPNState{State: &needsMachineAuth})

	snap := tn.Snapshot()
	if snap.State != NeedsMachineAuthState {
		t.Errorf("state = %q, want %q", snap.State, NeedsMachineAuthState)
	}
}

func TestTailnet_ReactToIPN_ConnectedToNeedsLogin(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)
	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(context.Background())

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	needsLogin := ipn.NeedsLogin
	loginURL := "https://login.tailscale.com/reauth"
	tn.reactToIPNStateChange(context.Background(), IPNState{State: &needsLogin, BrowseToURL: &loginURL})

	snap := tn.Snapshot()
	if snap.State != NeedsLoginState {
		t.Errorf("state = %q, want %q", snap.State, NeedsLoginState)
	}
}

func TestTailnet_ReactToIPN_NeedsLoginToConnected(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)
	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(context.Background())

	tn.mu.Lock()
	tn.currentState = NeedsLoginState
	tn.mu.Unlock()

	running := ipn.Running
	tn.reactToIPNStateChange(context.Background(), IPNState{State: &running})

	snap := tn.Snapshot()
	if snap.State != ConnectedState {
		t.Errorf("state = %q, want %q", snap.State, ConnectedState)
	}
}

func TestTailnet_ReactToIPN_ConnectedUpdatesPeers(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)
	if err := tn.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(context.Background())

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	peer := (&tailcfg.Node{ComputedName: "peer1"}).View()
	running := ipn.Running
	tn.reactToIPNStateChange(context.Background(), IPNState{
		State: &running,
		Peers: []tailcfg.NodeView{peer},
	})

	snap := tn.Snapshot()
	if len(snap.Peers) != 1 {
		t.Errorf("expected 1 peer, got %d", len(snap.Peers))
	}
}

func TestTailnet_ReactToIPN_ConnectedClaimsSuffix(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 0, obs, nil)
	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	running := ipn.Running
	tn.reactToIPNStateChange(context.Background(), IPNState{
		State:          &running,
		MagicDNSSuffix: "my-tailnet.ts.net",
	})

	snap := tn.Snapshot()
	if snap.MagicDNSSuffix != "my-tailnet.ts.net" {
		t.Errorf("suffix = %q, want %q", snap.MagicDNSSuffix, "my-tailnet.ts.net")
	}
}

func TestTailnet_ReactToIPN_UpdatesSelfNodeHostname(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "user-host", "", "", false, 0, obs, nil)
	tn.mu.Lock()
	tn.currentState = StartedState
	tn.mu.Unlock()

	selfNode := (&tailcfg.Node{ComputedName: "tailscale-host"}).View()
	needsLogin := ipn.NeedsLogin
	loginURL := "https://login.tailscale.com/x"
	tn.reactToIPNStateChange(context.Background(), IPNState{
		State:       &needsLogin,
		BrowseToURL: &loginURL,
		SelfNode:    selfNode,
	})

	snap := tn.Snapshot()
	if snap.Hostname != "tailscale-host" {
		t.Errorf("hostname = %q, want %q", snap.Hostname, "tailscale-host")
	}
}

func TestTailnet_ReactToIPN_IgnoredInStoppedState(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 0, obs, nil)

	running := ipn.Running
	tn.reactToIPNStateChange(context.Background(), IPNState{State: &running})

	snap := tn.Snapshot()
	if snap.State != StoppedState {
		t.Errorf("state = %q, want %q (should be ignored in Stopped)", snap.State, StoppedState)
	}
}

func TestTailnet_ReactToIPN_IgnoredInLoggingOutState(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 0, obs, nil)
	tn.mu.Lock()
	tn.currentState = LoggingOutState
	tn.mu.Unlock()

	running := ipn.Running
	tn.reactToIPNStateChange(context.Background(), IPNState{State: &running})

	snap := tn.Snapshot()
	if snap.State != LoggingOutState {
		t.Errorf("state = %q, want %q (should be ignored in LoggingOut)", snap.State, LoggingOutState)
	}
}

func TestTailnet_ReactToIPN_NeedsMachineAuthToConnected(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 0, obs, nil)
	tn.mu.Lock()
	tn.currentState = NeedsMachineAuthState
	tn.mu.Unlock()

	running := ipn.Running
	tn.reactToIPNStateChange(context.Background(), IPNState{State: &running})

	snap := tn.Snapshot()
	if snap.State != ConnectedState {
		t.Errorf("state = %q, want %q", snap.State, ConnectedState)
	}
}

func TestTailnet_ReactToIPN_NeedsMachineAuthClaimsSuffix(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 0, obs, nil)
	tn.mu.Lock()
	tn.currentState = NeedsMachineAuthState
	tn.mu.Unlock()

	needsMachineAuth := ipn.NeedsMachineAuth
	tn.reactToIPNStateChange(context.Background(), IPNState{
		State:          &needsMachineAuth,
		MagicDNSSuffix: "corp.ts.net",
	})

	snap := tn.Snapshot()
	if snap.MagicDNSSuffix != "corp.ts.net" {
		t.Errorf("suffix = %q, want %q", snap.MagicDNSSuffix, "corp.ts.net")
	}
}

func TestTailnet_ReactToIPN_ObserverNotifiedOnChange(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 0, obs, nil)
	tn.mu.Lock()
	tn.currentState = StartedState
	tn.mu.Unlock()

	initialCount := obs.callCount()

	running := ipn.Running
	tn.reactToIPNStateChange(context.Background(), IPNState{State: &running})

	if obs.callCount() <= initialCount {
		t.Error("expected observer to be notified on state change")
	}
}

func TestTailnet_ReactToIPN_NoNotifyWithoutChange(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 0, obs, nil)
	// Default state is StoppedState, IPN changes are ignored

	initialCount := obs.callCount()

	running := ipn.Running
	tn.reactToIPNStateChange(context.Background(), IPNState{State: &running})

	if obs.callCount() != initialCount {
		t.Error("expected no observer notification when IPN is ignored in StoppedState")
	}
}

// --- parseLogKeyValues ---

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
