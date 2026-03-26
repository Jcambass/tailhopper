package tailscale

import (
	"context"
	"fmt"
	"net"
	"testing"

	tsnetpkg "github.com/jcambass/tailhopper/internal/tsnet"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
)

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

	// Initially no peers
	snap := tn.Snapshot()
	if len(snap.Peers) != 0 {
		t.Errorf("expected 0 peers, got %d", len(snap.Peers))
	}

	// updatePeersLocked always returns true
	if !tn.updatePeersLocked(IPNState{}) {
		t.Error("expected true from updatePeersLocked")
	}
}

func TestTailnet_MaybeClaimMagicDNSSuffixLocked(t *testing.T) {
	obs := newMockObserver()

	t.Run("no suffix in IPN state", func(t *testing.T) {
		tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)
		if tn.maybeClaimMagicDNSSuffixLocked(IPNState{}) {
			t.Error("expected false with empty suffix")
		}
	})

	t.Run("already claimed", func(t *testing.T) {
		tn := NewTailnet(2, "/tmp", "host", "existing.ts.net", "", false, 1080, obs, nil)
		if tn.maybeClaimMagicDNSSuffixLocked(IPNState{MagicDNSSuffix: "existing.ts.net"}) {
			t.Error("expected false when already claimed with same suffix")
		}
	})

	t.Run("successful claim", func(t *testing.T) {
		tn := NewTailnet(3, "/tmp", "host", "", "", false, 1080, obs, nil)
		if !tn.maybeClaimMagicDNSSuffixLocked(IPNState{MagicDNSSuffix: "new-tailnet.ts.net"}) {
			t.Error("expected true on successful claim")
		}
		if tn.claimedMagicDNSSuffix != "new-tailnet.ts.net" {
			t.Errorf("claimedMagicDNSSuffix = %q, want %q", tn.claimedMagicDNSSuffix, "new-tailnet.ts.net")
		}
	})

	t.Run("duplicate claim from different tailnet", func(t *testing.T) {
		tn := NewTailnet(4, "/tmp", "host", "", "", false, 1080, obs, nil)
		// "new-tailnet.ts.net" is already claimed by tailnet 3
		changed := tn.maybeClaimMagicDNSSuffixLocked(IPNState{MagicDNSSuffix: "new-tailnet.ts.net"})
		if !changed {
			t.Error("expected true since terminal error is set")
		}
		if tn.currentState != HasTerminalErrorState {
			t.Errorf("state = %q, want %q", tn.currentState, HasTerminalErrorState)
		}
	})

	t.Run("suffix mismatch with existing claim", func(t *testing.T) {
		tn := NewTailnet(5, "/tmp", "host", "original.ts.net", "", false, 1080, obs, nil)
		// IPN reports a different suffix than what we claimed
		if tn.maybeClaimMagicDNSSuffixLocked(IPNState{MagicDNSSuffix: "different.ts.net"}) {
			t.Error("expected false when suffix mismatches (just logs error)")
		}
	})
}

func TestTailnet_SetTerminalErrorLocked(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", true, 1080, obs, nil)

	changed := tn.setTerminalErrorLocked("fatal: something bad")

	if !changed {
		t.Error("expected changed=true")
	}
	if tn.currentState != HasTerminalErrorState {
		t.Errorf("state = %q, want %q", tn.currentState, HasTerminalErrorState)
	}
	if tn.userState != UserDisabled {
		t.Errorf("user state = %q, want %q", tn.userState, UserDisabled)
	}
	if tn.terminalError != "fatal: something bad" {
		t.Errorf("terminal error = %q, want %q", tn.terminalError, "fatal: something bad")
	}
	if len(obs.userStateCalls) != 1 || obs.userStateCalls[0].state != UserDisabled {
		t.Errorf("expected user state callback with Disabled, got %v", obs.userStateCalls)
	}
	if len(obs.terminalErrCalls) != 1 || obs.terminalErrCalls[0].err != "fatal: something bad" {
		t.Errorf("expected terminal error callback, got %v", obs.terminalErrCalls)
	}
}

func TestTailnet_SetTerminalErrorLocked_Idempotent(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "already-errored", false, 1080, obs, nil)
	tn.currentState = HasTerminalErrorState

	changed := tn.setTerminalErrorLocked("already-errored")

	if changed {
		t.Error("expected changed=false for same terminal error")
	}
	if len(obs.terminalErrCalls) != 0 {
		t.Errorf("expected no callback, got %d calls", len(obs.terminalErrCalls))
	}
}

func TestTailnet_NotifyCallbacks(t *testing.T) {
	t.Run("observer broadcast is called", func(t *testing.T) {
		obs := newMockObserver()
		tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)
		tn.observer.OnBroadcast(tn.id)
		if len(obs.broadcastCalls) != 1 || obs.broadcastCalls[0] != 1 {
			t.Errorf("expected broadcast call for tailnet 1, got %v", obs.broadcastCalls)
		}
	})

	t.Run("uses a dummy noop observer", func(t *testing.T) {
		tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)
		tn.observer.OnBroadcast(tn.id) // should not panic
	})

	t.Run("user state change callback", func(t *testing.T) {
		obs := newMockObserver()
		tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)
		tn.observer.OnUserStateChange(tn.id, UserEnabled)
		if len(obs.userStateCalls) != 1 || obs.userStateCalls[0].state != UserEnabled {
			t.Errorf("expected UserEnabled callback, got %v", obs.userStateCalls)
		}
	})

	t.Run("terminal error change callback", func(t *testing.T) {
		obs := newMockObserver()
		tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)
		tn.observer.OnTerminalErrorChange(tn.id, "boom")
		if len(obs.terminalErrCalls) != 1 || obs.terminalErrCalls[0].err != "boom" {
			t.Errorf("expected 'boom' callback, got %v", obs.terminalErrCalls)
		}
	})
}

func TestTailnet_StartRequiresStoppedState(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)

	// Force state to something other than Stopped
	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	ctx := t.Context()
	err := tn.Start(ctx)
	if err == nil {
		t.Fatal("expected error when starting from non-stopped state")
	}
}

func TestTailnet_StopRequiresValidState(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)

	// StoppedState should not be stoppable
	ctx := t.Context()
	err := tn.Stop(ctx)
	if err == nil {
		t.Fatal("expected error when stopping from StoppedState")
	}
}

func TestTailnet_DialRequiresConnectedState(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)

	ctx := t.Context()
	_, err := tn.Dial(ctx, "tcp", "example.com:80")
	if err == nil {
		t.Fatal("expected error when dialing from StoppedState")
	}
}

func newStartableTailnet(t *testing.T, obs TailnetObserver) (*Tailnet, *tsnetpkg.MockTSNetServer) {
	t.Helper()
	mockServer := tsnetpkg.NewMockTSNetServer()

	// Set up WatchIPNBus to block until context is canceled so the watcher goroutine doesn't spin.
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

	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(t.Context())

	snap := tn.Snapshot()
	if snap.State != StartedState {
		t.Errorf("state = %q, want %q", snap.State, StartedState)
	}
	if snap.UserState != UserEnabled {
		t.Errorf("user state = %q, want %q", snap.UserState, UserEnabled)
	}
	// Observer should have been notified of user state change
	found := false
	for _, call := range obs.userStateCalls {
		if call.state == UserEnabled {
			found = true
		}
	}
	if !found {
		t.Error("expected OnUserStateChange(UserEnabled) callback")
	}
}

func TestTailnet_Start_SocksProxyFailure_Rollback(t *testing.T) {
	obs := newMockObserver()
	mockServer := tsnetpkg.NewMockTSNetServer()
	factory := func(config tsnetpkg.TSNetServerConfig) tsnetpkg.TSNetServer {
		return mockServer
	}

	// Use a port that is already occupied so SOCKS proxy creation fails
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	tn := NewTailnet(1, t.TempDir(), "host", "", "", false, port, obs, factory)

	err = tn.Start(t.Context())
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
		return fmt.Errorf("tsnet start failed")
	}
	factory := func(config tsnetpkg.TSNetServerConfig) tsnetpkg.TSNetServer {
		return mockServer
	}

	tn := NewTailnet(1, t.TempDir(), "host", "", "", false, 0, obs, factory)

	err := tn.Start(t.Context())
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

	err := tn.Start(t.Context())
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
	// Return a nil local client which will cause NewWatcher to fail
	mockServer.LocalClientFunc = func() (tsnetpkg.LocalClient, error) {
		return nil, nil // nil client, no error
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

	err := tn.Start(t.Context())
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

	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Simulate transition to connected
	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	if err := tn.Stop(t.Context()); err != nil {
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

	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := tn.Stop(t.Context()); err != nil {
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

	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	tn.mu.Lock()
	tn.currentState = NeedsLoginState
	tn.mu.Unlock()

	if err := tn.Stop(t.Context()); err != nil {
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

	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := tn.Stop(t.Context()); err != nil {
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

			err := tn.Stop(t.Context())
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

	if err := tn.Logout(t.Context()); err != nil {
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

	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	tn.mu.Lock()
	tn.currentState = NeedsLoginState
	tn.mu.Unlock()

	if err := tn.Logout(t.Context()); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	// State should remain NeedsLoginState since logout is a no-op here
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
	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	if err := tn.Logout(t.Context()); err != nil {
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

	err := tn.Logout(t.Context())
	if err == nil {
		t.Fatal("expected error from Logout")
	}

	// Regardless of error, should transition to Stopped
	snap := tn.Snapshot()
	if snap.State != StoppedState {
		t.Errorf("state = %q, want %q", snap.State, StoppedState)
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

			err := tn.Logout(t.Context())
			if err == nil {
				t.Errorf("expected error when logging out from %s", state)
			}
		})
	}
}

func TestTailnet_Logout_SetsLoggingOutState(t *testing.T) {
	obs := newMockObserver()
	stateObserved := make(chan State, 1)
	mockServer := tsnetpkg.NewMockTSNetServer()
	mockServer.LocalClientFunc = func() (tsnetpkg.LocalClient, error) {
		return &tsnetpkg.MockLocalClient{
			LogoutFunc: func(ctx context.Context) error {
				// Capture state during the logout call (while lock is released)
				snap := obs.broadcastCalls // Use the observer to verify state was set
				_ = snap
				return nil
			},
		}, nil
	}
	factory := func(config tsnetpkg.TSNetServerConfig) tsnetpkg.TSNetServer {
		return mockServer
	}

	tn := NewTailnet(1, t.TempDir(), "host", "", "", false, 0, obs, factory)

	// Override LocalClient to capture the state during logout
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

	if err := tn.Logout(t.Context()); err != nil {
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
	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(t.Context())

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	_, err := tn.Dial(t.Context(), "tcp", "example.com:80")
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

	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer tn.Stop(t.Context())

	// Second start should fail since we're now in StartedState
	err := tn.Start(t.Context())
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
		// Return a fresh mock each time so Close() doesn't interfere with restart.
		m := tsnetpkg.NewMockTSNetServer()
		m.LocalClientFunc = mockServer.LocalClientFunc
		return m
	}

	tn := NewTailnet(1, t.TempDir(), "host", "", "", false, 0, obs, factory)

	// First cycle
	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := tn.Stop(t.Context()); err != nil {
		t.Fatalf("first Stop: %v", err)
	}

	// Second cycle
	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if err := tn.Stop(t.Context()); err != nil {
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

	// Set selfNodeHostname
	tn.mu.Lock()
	tn.selfNodeHostname = "actual-host"
	tn.mu.Unlock()

	snap = tn.Snapshot()
	if snap.Hostname != "actual-host" {
		t.Errorf("hostname = %q, want %q (self-node)", snap.Hostname, "actual-host")
	}
}

func TestTailnet_ReactToIPN_StartedToConnected(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)
	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(t.Context())

	running := ipn.Running
	tn.reactToIPNStateChange(t.Context(), IPNState{State: &running})

	snap := tn.Snapshot()
	if snap.State != ConnectedState {
		t.Errorf("state = %q, want %q", snap.State, ConnectedState)
	}
}

func TestTailnet_ReactToIPN_StartedToNeedsLogin(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)
	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(t.Context())

	needsLogin := ipn.NeedsLogin
	loginURL := "https://login.tailscale.com/abc"
	tn.reactToIPNStateChange(t.Context(), IPNState{State: &needsLogin, BrowseToURL: &loginURL})

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
	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(t.Context())

	needsMachineAuth := ipn.NeedsMachineAuth
	tn.reactToIPNStateChange(t.Context(), IPNState{State: &needsMachineAuth})

	snap := tn.Snapshot()
	if snap.State != NeedsMachineAuthState {
		t.Errorf("state = %q, want %q", snap.State, NeedsMachineAuthState)
	}
}

func TestTailnet_ReactToIPN_ConnectedToNeedsLogin(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)
	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(t.Context())

	// First transition to Connected
	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	needsLogin := ipn.NeedsLogin
	loginURL := "https://login.tailscale.com/reauth"
	tn.reactToIPNStateChange(t.Context(), IPNState{State: &needsLogin, BrowseToURL: &loginURL})

	snap := tn.Snapshot()
	if snap.State != NeedsLoginState {
		t.Errorf("state = %q, want %q", snap.State, NeedsLoginState)
	}
}

func TestTailnet_ReactToIPN_NeedsLoginToConnected(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)
	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(t.Context())

	tn.mu.Lock()
	tn.currentState = NeedsLoginState
	tn.mu.Unlock()

	running := ipn.Running
	tn.reactToIPNStateChange(t.Context(), IPNState{State: &running})

	snap := tn.Snapshot()
	if snap.State != ConnectedState {
		t.Errorf("state = %q, want %q", snap.State, ConnectedState)
	}
}

func TestTailnet_ReactToIPN_ConnectedUpdatesPeers(t *testing.T) {
	obs := newMockObserver()
	tn, _ := newStartableTailnet(t, obs)
	if err := tn.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tn.Stop(t.Context())

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	peer := (&tailcfg.Node{ComputedName: "peer1"}).View()
	running := ipn.Running
	tn.reactToIPNStateChange(t.Context(), IPNState{
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
	tn.reactToIPNStateChange(t.Context(), IPNState{
		State:          &running,
		MagicDNSSuffix: "my-tailnet.ts.net",
	})

	snap := tn.Snapshot()
	if snap.MagicDNSSuffix != "my-tailnet.ts.net" {
		t.Errorf("suffix = %q, want %q", snap.MagicDNSSuffix, "my-tailnet.ts.net")
	}
}

func TestTailnet_ReactToIPN_DuplicateClaimCausesTerminalError(t *testing.T) {
	obs := newMockObserver()
	// Pre-claim "my-tailnet.ts.net" for another tailnet
	obs.claims["my-tailnet.ts.net"] = 99

	tn := NewTailnet(1, "/tmp", "host", "", "", false, 0, obs, nil)
	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	running := ipn.Running
	tn.reactToIPNStateChange(t.Context(), IPNState{
		State:          &running,
		MagicDNSSuffix: "my-tailnet.ts.net",
	})

	snap := tn.Snapshot()
	if snap.State != HasTerminalErrorState {
		t.Errorf("state = %q, want %q", snap.State, HasTerminalErrorState)
	}
	if snap.TerminalError == "" {
		t.Error("expected non-empty terminal error")
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
	tn.reactToIPNStateChange(t.Context(), IPNState{
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
	// Default state is StoppedState

	running := ipn.Running
	tn.reactToIPNStateChange(t.Context(), IPNState{State: &running})

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
	tn.reactToIPNStateChange(t.Context(), IPNState{State: &running})

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
	tn.reactToIPNStateChange(t.Context(), IPNState{State: &running})

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
	tn.reactToIPNStateChange(t.Context(), IPNState{
		State:          &needsMachineAuth,
		MagicDNSSuffix: "corp.ts.net",
	})

	snap := tn.Snapshot()
	if snap.MagicDNSSuffix != "corp.ts.net" {
		t.Errorf("suffix = %q, want %q", snap.MagicDNSSuffix, "corp.ts.net")
	}
}

func TestTailnet_ReactToIPN_BroadcastOnChange(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 0, obs, nil)
	tn.mu.Lock()
	tn.currentState = StartedState
	tn.mu.Unlock()

	initialBroadcasts := len(obs.broadcastCalls)

	running := ipn.Running
	tn.reactToIPNStateChange(t.Context(), IPNState{State: &running})

	if len(obs.broadcastCalls) <= initialBroadcasts {
		t.Error("expected broadcast after state change")
	}
}

func TestTailnet_ReactToIPN_NoBroadcastWithoutChange(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 0, obs, nil)
	tn.mu.Lock()
	tn.currentState = StartedState
	tn.mu.Unlock()

	initialBroadcasts := len(obs.broadcastCalls)

	// Send IPN state with no meaningful transition
	starting := ipn.Starting
	tn.reactToIPNStateChange(t.Context(), IPNState{State: &starting})

	if len(obs.broadcastCalls) != initialBroadcasts {
		t.Error("expected no broadcast when no state change occurred")
	}
}
