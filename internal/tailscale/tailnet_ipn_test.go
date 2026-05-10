package tailscale

import (
	"context"
	"net"
	"testing"

	tsnetpkg "github.com/jcambass/tailhopper/internal/tsnet"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
)

// helper to build an IPNState with a specific ipn.State.
func ipnStateWith(state ipn.State) IPNState {
	return IPNState{State: &state}
}

// helper to build an IPNState with state + BrowseToURL.
func ipnStateWithLogin(url string) IPNState {
	needsLogin := ipn.NeedsLogin
	return IPNState{State: &needsLogin, BrowseToURL: &url}
}

// --- reactToIPNStateChange: from StartedState ---

func TestReactToIPN_Started_TransitionsToNeedsLogin(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = StartedState
	tn.mu.Unlock()

	url := "https://login.tailscale.com/abc"
	tn.reactToIPNStateChange(context.Background(), ipnStateWithLogin(url))

	snap := tn.Snapshot()
	if snap.State != NeedsLoginState {
		t.Errorf("state = %q, want %q", snap.State, NeedsLoginState)
	}
	if snap.LoginURL != url {
		t.Errorf("loginURL = %q, want %q", snap.LoginURL, url)
	}
	if obs.callCount() != 1 {
		t.Errorf("observer called %d times, want 1", obs.callCount())
	}
}

func TestReactToIPN_Started_TransitionsToNeedsMachineAuth(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = StartedState
	tn.mu.Unlock()

	tn.reactToIPNStateChange(context.Background(), ipnStateWith(ipn.NeedsMachineAuth))

	snap := tn.Snapshot()
	if snap.State != NeedsMachineAuthState {
		t.Errorf("state = %q, want %q", snap.State, NeedsMachineAuthState)
	}
}

func TestReactToIPN_Started_TransitionsToConnected(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = StartedState
	tn.mu.Unlock()

	tn.reactToIPNStateChange(context.Background(), ipnStateWith(ipn.Running))

	snap := tn.Snapshot()
	if snap.State != ConnectedState {
		t.Errorf("state = %q, want %q", snap.State, ConnectedState)
	}
}

func TestReactToIPN_Started_UpdatesSelfNodeHostname(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = StartedState
	tn.mu.Unlock()

	selfNode := (&tailcfg.Node{ComputedName: "my-node"}).View()
	tn.reactToIPNStateChange(context.Background(), IPNState{SelfNode: selfNode})

	snap := tn.Snapshot()
	if snap.Hostname != "my-node" {
		t.Errorf("hostname = %q, want %q", snap.Hostname, "my-node")
	}
}

func TestReactToIPN_Started_NoTransitionOnIrrelevantState(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = StartedState
	tn.mu.Unlock()

	// Starting state doesn't match any transition
	tn.reactToIPNStateChange(context.Background(), ipnStateWith(ipn.Starting))

	snap := tn.Snapshot()
	if snap.State != StartedState {
		t.Errorf("state = %q, want %q (should not transition)", snap.State, StartedState)
	}
	if obs.callCount() != 0 {
		t.Errorf("observer called %d times, want 0 (no change)", obs.callCount())
	}
}

// --- reactToIPNStateChange: from ConnectedState ---

func TestReactToIPN_Connected_TransitionsToNeedsLogin(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	url := "https://login.tailscale.com/reauth"
	tn.reactToIPNStateChange(context.Background(), ipnStateWithLogin(url))

	snap := tn.Snapshot()
	if snap.State != NeedsLoginState {
		t.Errorf("state = %q, want %q", snap.State, NeedsLoginState)
	}
}

func TestReactToIPN_Connected_TransitionsToNeedsMachineAuth(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	tn.reactToIPNStateChange(context.Background(), ipnStateWith(ipn.NeedsMachineAuth))

	snap := tn.Snapshot()
	if snap.State != NeedsMachineAuthState {
		t.Errorf("state = %q, want %q", snap.State, NeedsMachineAuthState)
	}
}

func TestReactToIPN_Connected_UpdatesPeers(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	peers := []tailcfg.NodeView{
		(&tailcfg.Node{ComputedName: "peer1"}).View(),
		(&tailcfg.Node{ComputedName: "peer2"}).View(),
	}

	tn.reactToIPNStateChange(context.Background(), IPNState{Peers: peers})

	snap := tn.Snapshot()
	if len(snap.Peers) != 2 {
		t.Errorf("peers = %d, want 2", len(snap.Peers))
	}
	// updatePeersLocked always returns true, so observer should fire
	if obs.callCount() < 1 {
		t.Error("expected observer to be called after peer update")
	}
}

func TestReactToIPN_Connected_ClaimsMagicDNSSuffix(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	tn.reactToIPNStateChange(context.Background(), IPNState{MagicDNSSuffix: "my-tailnet.ts.net"})

	snap := tn.Snapshot()
	if snap.MagicDNSSuffix != "my-tailnet.ts.net" {
		t.Errorf("MagicDNSSuffix = %q, want %q", snap.MagicDNSSuffix, "my-tailnet.ts.net")
	}
}

func TestReactToIPN_Connected_DoesNotTransitionToConnected(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	// Running is NOT in the OneOf list for Connected state
	tn.reactToIPNStateChange(context.Background(), ipnStateWith(ipn.Running))

	snap := tn.Snapshot()
	if snap.State != ConnectedState {
		t.Errorf("state = %q, want %q", snap.State, ConnectedState)
	}
}

func TestReactToIPN_Connected_UpdatesSelfNodeHostname(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	selfNode := (&tailcfg.Node{ComputedName: "updated-host"}).View()
	tn.reactToIPNStateChange(context.Background(), IPNState{SelfNode: selfNode})

	snap := tn.Snapshot()
	if snap.Hostname != "updated-host" {
		t.Errorf("hostname = %q, want %q", snap.Hostname, "updated-host")
	}
}

// --- reactToIPNStateChange: from NeedsLoginState ---

func TestReactToIPN_NeedsLogin_TransitionsToConnected(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = NeedsLoginState
	tn.mu.Unlock()

	tn.reactToIPNStateChange(context.Background(), ipnStateWith(ipn.Running))

	snap := tn.Snapshot()
	if snap.State != ConnectedState {
		t.Errorf("state = %q, want %q", snap.State, ConnectedState)
	}
}

func TestReactToIPN_NeedsLogin_TransitionsToNeedsMachineAuth(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = NeedsLoginState
	tn.mu.Unlock()

	tn.reactToIPNStateChange(context.Background(), ipnStateWith(ipn.NeedsMachineAuth))

	snap := tn.Snapshot()
	if snap.State != NeedsMachineAuthState {
		t.Errorf("state = %q, want %q", snap.State, NeedsMachineAuthState)
	}
}

func TestReactToIPN_NeedsLogin_DoesNotTransitionBackToNeedsLogin(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = NeedsLoginState
	tn.loginURL = "https://existing-url"
	tn.mu.Unlock()

	// NeedsLogin -> NeedsLogin is NOT in the OneOf list
	url := "https://login.tailscale.com/new-url"
	tn.reactToIPNStateChange(context.Background(), ipnStateWithLogin(url))

	snap := tn.Snapshot()
	if snap.State != NeedsLoginState {
		t.Errorf("state = %q, want %q", snap.State, NeedsLoginState)
	}
	// loginURL should NOT be updated since NeedsLogin transition isn't in OneOf
	if snap.LoginURL != "https://existing-url" {
		t.Errorf("loginURL = %q, want %q (should not update)", snap.LoginURL, "https://existing-url")
	}
}

func TestReactToIPN_NeedsLogin_UpdatesSelfNodeHostname(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = NeedsLoginState
	tn.mu.Unlock()

	selfNode := (&tailcfg.Node{ComputedName: "login-host"}).View()
	tn.reactToIPNStateChange(context.Background(), IPNState{SelfNode: selfNode})

	snap := tn.Snapshot()
	if snap.Hostname != "login-host" {
		t.Errorf("hostname = %q, want %q", snap.Hostname, "login-host")
	}
}

// --- reactToIPNStateChange: from NeedsMachineAuthState ---

func TestReactToIPN_NeedsMachineAuth_TransitionsToConnected(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = NeedsMachineAuthState
	tn.mu.Unlock()

	tn.reactToIPNStateChange(context.Background(), ipnStateWith(ipn.Running))

	snap := tn.Snapshot()
	if snap.State != ConnectedState {
		t.Errorf("state = %q, want %q", snap.State, ConnectedState)
	}
}

func TestReactToIPN_NeedsMachineAuth_TransitionsToNeedsLogin(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = NeedsMachineAuthState
	tn.mu.Unlock()

	url := "https://login.tailscale.com/reauth"
	tn.reactToIPNStateChange(context.Background(), ipnStateWithLogin(url))

	snap := tn.Snapshot()
	if snap.State != NeedsLoginState {
		t.Errorf("state = %q, want %q", snap.State, NeedsLoginState)
	}
}

func TestReactToIPN_NeedsMachineAuth_ClaimsMagicDNSSuffix(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = NeedsMachineAuthState
	tn.mu.Unlock()

	tn.reactToIPNStateChange(context.Background(), IPNState{MagicDNSSuffix: "corp.ts.net"})

	snap := tn.Snapshot()
	if snap.MagicDNSSuffix != "corp.ts.net" {
		t.Errorf("MagicDNSSuffix = %q, want %q", snap.MagicDNSSuffix, "corp.ts.net")
	}
}

func TestReactToIPN_NeedsMachineAuth_DoesNotTransitionBackToNeedsMachineAuth(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = NeedsMachineAuthState
	tn.mu.Unlock()

	// NeedsMachineAuth is NOT in the OneOf list for NeedsMachineAuthState
	tn.reactToIPNStateChange(context.Background(), ipnStateWith(ipn.NeedsMachineAuth))

	snap := tn.Snapshot()
	if snap.State != NeedsMachineAuthState {
		t.Errorf("state = %q, want %q", snap.State, NeedsMachineAuthState)
	}
}

// --- reactToIPNStateChange: from ignored states ---

func TestReactToIPN_StoppedState_IgnoresIPNEvents(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	// Default state is StoppedState
	tn.reactToIPNStateChange(context.Background(), ipnStateWith(ipn.Running))

	snap := tn.Snapshot()
	if snap.State != StoppedState {
		t.Errorf("state = %q, want %q", snap.State, StoppedState)
	}
	if obs.callCount() != 0 {
		t.Errorf("observer called %d times, want 0", obs.callCount())
	}
}

func TestReactToIPN_HasTerminalError_IgnoresIPNEvents(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "fatal", false, 1080, obs, nil)

	tn.reactToIPNStateChange(context.Background(), ipnStateWith(ipn.Running))

	snap := tn.Snapshot()
	if snap.State != HasTerminalErrorState {
		t.Errorf("state = %q, want %q", snap.State, HasTerminalErrorState)
	}
	if obs.callCount() != 0 {
		t.Errorf("observer called %d times, want 0", obs.callCount())
	}
}

func TestReactToIPN_LoggingOutState_IgnoresIPNEvents(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = LoggingOutState
	tn.mu.Unlock()

	tn.reactToIPNStateChange(context.Background(), ipnStateWith(ipn.Running))

	snap := tn.Snapshot()
	if snap.State != LoggingOutState {
		t.Errorf("state = %q, want %q", snap.State, LoggingOutState)
	}
	if obs.callCount() != 0 {
		t.Errorf("observer called %d times, want 0", obs.callCount())
	}
}

// --- reactToIPNStateChange: observer notification ---

func TestReactToIPN_NotifiesObserverOnlyOnChange(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = StartedState
	tn.mu.Unlock()

	// Starting state doesn't trigger any transition or Always handler change
	tn.reactToIPNStateChange(context.Background(), ipnStateWith(ipn.Starting))
	if obs.callCount() != 0 {
		t.Errorf("observer called %d times after no-op, want 0", obs.callCount())
	}

	// Running triggers transition to Connected
	tn.reactToIPNStateChange(context.Background(), ipnStateWith(ipn.Running))
	if obs.callCount() != 1 {
		t.Errorf("observer called %d times after transition, want 1", obs.callCount())
	}

	last, _ := obs.lastSnapshot()
	if last.State != ConnectedState {
		t.Errorf("observer snapshot state = %q, want %q", last.State, ConnectedState)
	}
}

// --- reactToIPNStateChange: OneOf short-circuit ---

func TestReactToIPN_Started_OneOfStopsAfterFirstMatch(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = StartedState
	tn.mu.Unlock()

	// NeedsLogin is checked before NeedsMachineAuth and Connected in the OneOf
	// chain for StartedState. Send NeedsLogin — it should match and not
	// continue to Connected.
	url := "https://login.tailscale.com/test"
	needsLogin := ipn.NeedsLogin
	tn.reactToIPNStateChange(context.Background(), IPNState{
		State:       &needsLogin,
		BrowseToURL: &url,
	})

	snap := tn.Snapshot()
	if snap.State != NeedsLoginState {
		t.Errorf("state = %q, want %q", snap.State, NeedsLoginState)
	}
}

// --- reactToIPNStateChange: combined Always + OneOf ---

func TestReactToIPN_Connected_AlwaysAndOneOfCombined(t *testing.T) {
	obs := newMockObserver()
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, obs, nil)

	tn.mu.Lock()
	tn.currentState = ConnectedState
	tn.mu.Unlock()

	// Send a rich event with suffix + peers + hostname + NeedsLogin transition
	url := "https://login.tailscale.com/combined"
	needsLogin := ipn.NeedsLogin
	selfNode := (&tailcfg.Node{ComputedName: "combined-host"}).View()
	peers := []tailcfg.NodeView{
		(&tailcfg.Node{ComputedName: "peer1"}).View(),
	}

	tn.reactToIPNStateChange(context.Background(), IPNState{
		State:          &needsLogin,
		BrowseToURL:    &url,
		MagicDNSSuffix: "combined.ts.net",
		SelfNode:       selfNode,
		Peers:          peers,
	})

	snap := tn.Snapshot()
	// OneOf should trigger NeedsLogin transition
	if snap.State != NeedsLoginState {
		t.Errorf("state = %q, want %q", snap.State, NeedsLoginState)
	}
	// Always handlers should have run: suffix claimed, peers updated, hostname updated
	if snap.MagicDNSSuffix != "combined.ts.net" {
		t.Errorf("MagicDNSSuffix = %q, want %q", snap.MagicDNSSuffix, "combined.ts.net")
	}
	if len(snap.Peers) != 1 {
		t.Errorf("peers = %d, want 1", len(snap.Peers))
	}
	if snap.Hostname != "combined-host" {
		t.Errorf("hostname = %q, want %q", snap.Hostname, "combined-host")
	}
}

// --- terminalCleanup ---

func TestPrepareTerminalErrorCleanupLocked_NilsOutComponents(t *testing.T) {
	tn := NewTailnet(1, "/tmp", "host", "", "", false, 1080, nil, nil)

	// Set some fake components
	tn.mu.Lock()
	tn.server = &fakeServer{}
	tn.watcher = &watcher{}
	cleanup := tn.prepareTerminalErrorCleanupLocked()
	tn.mu.Unlock()

	// After preparation, tailnet's fields should be nil
	tn.mu.RLock()
	if tn.server != nil {
		t.Error("expected server to be nil after prepare")
	}
	if tn.watcher != nil {
		t.Error("expected watcher to be nil after prepare")
	}
	if tn.socksProxy != nil {
		t.Error("expected socksProxy to be nil after prepare")
	}
	tn.mu.RUnlock()

	// cleanup struct should hold the originals
	if cleanup.server == nil {
		t.Error("expected cleanup.server to be non-nil")
	}
	if cleanup.watcher == nil {
		t.Error("expected cleanup.watcher to be non-nil")
	}
}

// fakeServer is a minimal TSNetServer for cleanup tests.
type fakeServer struct{}

func (f *fakeServer) Start() error                                                     { return nil }
func (f *fakeServer) Close() error                                                     { return nil }
func (f *fakeServer) Dial(_ context.Context, _, _ string) (net.Conn, error)            { return nil, nil }
func (f *fakeServer) LocalClient() (tsnetpkg.LocalClient, error)                       { return nil, nil }
