package tailscale

import (
	"testing"

	"tailscale.com/ipn"
)

// mockBroadcaster records broadcast calls for testing.
type mockBroadcaster struct {
	calls int
}

func (m *mockBroadcaster) broadcast() {
	m.calls++
}

func TestNewTailnet_Defaults(t *testing.T) {
	tn := NewTailnet(1, "/tmp/state", "my-host", "", "", false, 1080, nil, nil, nil, nil, nil)

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
	tn := NewTailnet(2, "/tmp/state", "host", "", "", true, 1081, nil, nil, nil, nil, nil)

	snap := tn.Snapshot()
	if snap.UserState != UserEnabled {
		t.Errorf("user state = %q, want %q", snap.UserState, UserEnabled)
	}
	if snap.State != StoppedState {
		t.Errorf("state = %q, want %q", snap.State, StoppedState)
	}
}

func TestNewTailnet_WithTerminalError(t *testing.T) {
	tn := NewTailnet(3, "/tmp/state", "host", "", "fatal error", true, 1082, nil, nil, nil, nil, nil)

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
	tn := NewTailnet(4, "/tmp/state", "host", "my-tailnet.ts.net", "", false, 1083, nil, nil, nil, nil, nil)

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
		tn := NewTailnet(1, "/tmp", "host", "", "", false, tt.port, nil, nil, nil, nil, nil)
		if tn.SocksAddr() != tt.want {
			t.Errorf("SocksAddr() with port %d = %q, want %q", tt.port, tn.SocksAddr(), tt.want)
		}
	}
}

func TestTailnet_MaybeTransitionToNeedsLoginLocked(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil, nil, nil, nil)

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
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil, nil, nil, nil)

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
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil, nil, nil, nil)

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
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil, nil, nil, nil)

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
	registry := newMockSuffixRegistry()

	t.Run("no suffix in IPN state", func(t *testing.T) {
		tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, registry, nil, nil, nil, nil)
		if tn.maybeClaimMagicDNSSuffixLocked(IPNState{}) {
			t.Error("expected false with empty suffix")
		}
	})

	t.Run("already claimed", func(t *testing.T) {
		tn := NewTailnet(2, "/tmp", "host", "existing.ts.net", "", false, 1080, registry, nil, nil, nil, nil)
		if tn.maybeClaimMagicDNSSuffixLocked(IPNState{MagicDNSSuffix: "existing.ts.net"}) {
			t.Error("expected false when already claimed with same suffix")
		}
	})

	t.Run("successful claim", func(t *testing.T) {
		tn := NewTailnet(3, "/tmp", "host", "", "", false, 1080, registry, nil, nil, nil, nil)
		if !tn.maybeClaimMagicDNSSuffixLocked(IPNState{MagicDNSSuffix: "new-tailnet.ts.net"}) {
			t.Error("expected true on successful claim")
		}
		if tn.claimedMagicDNSSuffix != "new-tailnet.ts.net" {
			t.Errorf("claimedMagicDNSSuffix = %q, want %q", tn.claimedMagicDNSSuffix, "new-tailnet.ts.net")
		}
	})

	t.Run("duplicate claim from different tailnet", func(t *testing.T) {
		tn := NewTailnet(4, "/tmp", "host", "", "", false, 1080, registry, nil, nil, nil, nil)
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
		tn := NewTailnet(5, "/tmp", "host", "original.ts.net", "", false, 1080, registry, nil, nil, nil, nil)
		// IPN reports a different suffix than what we claimed
		if tn.maybeClaimMagicDNSSuffixLocked(IPNState{MagicDNSSuffix: "different.ts.net"}) {
			t.Error("expected false when suffix mismatches (just logs error)")
		}
	})
}

func TestTailnet_SetTerminalErrorLocked(t *testing.T) {
	var userStateCalls []UserState
	var terminalErrorCalls []string

	tn := NewTailnet(1, "/tmp", "host", "", "", true, 1080, nil, nil,
		func(s UserState) { userStateCalls = append(userStateCalls, s) },
		func(err string) { terminalErrorCalls = append(terminalErrorCalls, err) },
		nil,
	)

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
	if len(userStateCalls) != 1 || userStateCalls[0] != UserDisabled {
		t.Errorf("expected user state callback with Disabled, got %v", userStateCalls)
	}
	if len(terminalErrorCalls) != 1 || terminalErrorCalls[0] != "fatal: something bad" {
		t.Errorf("expected terminal error callback, got %v", terminalErrorCalls)
	}
}

func TestTailnet_SetTerminalErrorLocked_Idempotent(t *testing.T) {
	callCount := 0
	tn := NewTailnet(1, "/tmp", "host", "", "already-errored", false, 1080, nil, nil, nil,
		func(err string) { callCount++ },
		nil,
	)
	tn.currentState = HasTerminalErrorState

	changed := tn.setTerminalErrorLocked("already-errored")

	if changed {
		t.Error("expected changed=false for same terminal error")
	}
	if callCount != 0 {
		t.Errorf("expected no callback, got %d calls", callCount)
	}
}

func TestTailnet_NotifyCallbacks(t *testing.T) {
	t.Run("broadcast callback is called", func(t *testing.T) {
		b := &mockBroadcaster{}
		tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, b.broadcast, nil, nil, nil)
		tn.notify()
		if b.calls != 1 {
			t.Errorf("expected 1 broadcast call, got %d", b.calls)
		}
	})

	t.Run("nil broadcast doesn't panic", func(t *testing.T) {
		tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil, nil, nil, nil)
		tn.notify() // should not panic
	})

	t.Run("user state change callback", func(t *testing.T) {
		var called UserState
		tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil,
			func(s UserState) { called = s }, nil, nil)
		tn.notifyUserStateChange(UserEnabled)
		if called != UserEnabled {
			t.Errorf("expected UserEnabled, got %q", called)
		}
	})

	t.Run("terminal error change callback", func(t *testing.T) {
		var called string
		tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil, nil,
			func(err string) { called = err }, nil)
		tn.notifyTerminalErrorChange("boom")
		if called != "boom" {
			t.Errorf("expected 'boom', got %q", called)
		}
	})
}

func TestTailnet_StartRequiresStoppedState(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil, nil, nil, nil)

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
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil, nil, nil, nil)

	// StoppedState should not be stoppable
	ctx := t.Context()
	err := tn.Stop(ctx)
	if err == nil {
		t.Fatal("expected error when stopping from StoppedState")
	}
}

func TestTailnet_DialRequiresConnectedState(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil, nil, nil, nil)

	ctx := t.Context()
	_, err := tn.Dial(ctx, "tcp", "example.com:80")
	if err == nil {
		t.Fatal("expected error when dialing from StoppedState")
	}
}
