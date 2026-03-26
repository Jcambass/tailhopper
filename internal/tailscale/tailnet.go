package tailscale

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"

	"github.com/jcambass/tailhopper/internal/socks"
	tsnetpkg "github.com/jcambass/tailhopper/tsnet"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
)

type Tailnet struct {
	// Immutable after construction.
	id                     int
	socksPort              int
	tsnetStateDir          string
	userSetHostname        string
	magicDNSSuffixRegistry MagicDNSSuffixRegistry
	broadcast              func()
	newServer              tsnetpkg.TSNetServerFactory
	// onUserStateChange, if non-nil, is called whenever the user's desired state changes.
	onUserStateChange func(UserState)
	// onTerminalErrorChange, if non-nil, is called whenever a fatal terminal error is set.
	onTerminalErrorChange func(string)

	// Logger has its own lock to avoid blocking state reads.
	logMu  sync.RWMutex
	logger *slog.Logger

	mu sync.RWMutex

	// Only changed by commands (start, stop, logout).
	server     tsnetpkg.TSNetServer
	watcher    *watcher
	socksProxy *socks.Server

	// Also changed by IPN state changes.
	claimedMagicDNSSuffix string
	terminalError         string
	peers                 []tailcfg.NodeView
	loginURL              string
	currentState          State
	// selfNodeHostname is the hostname reported by Tailscale for our own node.
	// Populated once the NetMap is received; empty until then.
	selfNodeHostname string

	// userState tracks the user's desired on/off state, independent of the
	// internal connection state managed by Tailscale IPN events.
	userState UserState
}

type TailnetSnapshot struct {
	ID             int
	State          State
	UserState      UserState
	MagicDNSSuffix string
	Hostname       string
	LoginURL       string
	Peers          []tailcfg.NodeView
	TerminalError  string
}

func (s *TailnetSnapshot) String() string {
	return fmt.Sprintf("TailnetSnapshot{ID: %d, State: %s, UserState: %s, MagicDNSSuffix: %s, Hostname: %s, LoginURL: %s, Peers: %d, TerminalError: %s}", s.ID, s.State, s.UserState, s.MagicDNSSuffix, s.Hostname, s.LoginURL, len(s.Peers), s.TerminalError)
}

func NewTailnet(id int, tsnetStateDir string, hostname string, claimedMagicDNSSuffix string, terminalError string, userEnabled bool, socksPort int, magicDNSSuffixRegistry MagicDNSSuffixRegistry, broadcast func(), onUserStateChange func(UserState), onTerminalErrorChange func(string), newServer tsnetpkg.TSNetServerFactory) *Tailnet {
	t := &Tailnet{
		id:                     id,
		magicDNSSuffixRegistry: magicDNSSuffixRegistry,
		broadcast:              broadcast,
		newServer:              newServer,
		onUserStateChange:      onUserStateChange,
		onTerminalErrorChange:  onTerminalErrorChange,
		logger:                 slog.Default().With("component", "tailnet", "tailnet_id", id),
		tsnetStateDir:          tsnetStateDir,
		userSetHostname:        hostname,
		socksPort:              socksPort,
		claimedMagicDNSSuffix:  claimedMagicDNSSuffix,
		terminalError:          terminalError,
	}

	if claimedMagicDNSSuffix != "" {
		t.logger = t.logger.With("magic_dns_suffix", claimedMagicDNSSuffix)
	}

	if terminalError != "" {
		t.currentState = HasTerminalErrorState
		t.userState = UserDisabled
	} else {
		t.currentState = StoppedState
		if userEnabled {
			t.userState = UserEnabled
		} else {
			t.userState = UserDisabled
		}
	}

	return t
}

func (t *Tailnet) ID() int {
	return t.id
}

func (t *Tailnet) SocksAddr() string {
	return fmt.Sprintf("localhost:%d", t.socksPort)
}

func (t *Tailnet) Snapshot() TailnetSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	hostname := t.userSetHostname
	if t.selfNodeHostname != "" {
		hostname = t.selfNodeHostname
	}

	return TailnetSnapshot{
		ID:             t.id,
		MagicDNSSuffix: t.claimedMagicDNSSuffix,
		Hostname:       hostname,
		LoginURL:       t.loginURL,
		Peers:          t.peers,
		TerminalError:  t.terminalError,
		State:          t.currentState,
		UserState:      t.userState,
	}
}

// Commands
func (t *Tailnet) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.currentState != StoppedState {
		return fmt.Errorf("unable to start: tailnet is in state %s", t.currentState)
	}

	// Record user's intent to enable the tailnet.
	t.userState = UserEnabled
	t.notifyUserStateChange(t.userState)

	t.log().Debug("Starting tailnet")

	t.log().Debug("Starting SOCKS5 proxy", slog.Int("port", t.socksPort))
	socksProxy, err := socks.NewServer(t.Dial, t.socksPort)
	if err != nil {
		t.log().Error("failed to start SOCKS5 proxy", slog.Any("error", err))
		// At this point we haven't started any long-running processes, so we can just return the error without worrying about cleanup.
		// TODO: Give some UI feedback that the server failed to start and the tailnet is non-functional, since the user might not understand why it's auto stopping.
		t.setState(StoppedState)
		return err
	}
	socksProxy.Start()

	// Asynchronously start the server
	t.log().Debug("Starting tsnet server")

	server := t.newServer(tsnetpkg.TSNetServerConfig{
		Dir:      t.tsnetStateDir,
		Hostname: t.userSetHostname,
		Logf:     t.tsnetLogf(slog.LevelDebug),
		UserLogf: t.tsnetLogf(slog.LevelInfo),
	})

	err = server.Start()
	if err != nil {
		t.log().Error("failed to start tsnet server", slog.Any("error", err))
		// If we fail to start the server, we should stop the socks proxy that we started since they won't be functional without the server.

		closeErr := socksProxy.Close()
		if closeErr != nil {
			t.log().Error("failed to close SOCKS5 proxy after server start failure", slog.Any("error", closeErr))
		}
		// TODO: Give some UI feedback that the server failed to start and the tailnet is non-functional, since the user might not understand why it's auto stopping.
		t.setState(StoppedState)
		return err
	}

	lc, err := server.LocalClient()
	if err != nil {
		t.log().Error("failed to get LocalClient for watcher", slog.Any("error", err))
		closeErr := socksProxy.Close()
		if closeErr != nil {
			t.log().Error("failed to close SOCKS5 proxy after LocalClient failure", slog.Any("error", closeErr))
		}
		err = server.Close()
		if err != nil {
			t.log().Error("failed to close tsnet server after LocalClient failure", slog.Any("error", err))
		}
		t.setState(StoppedState)
		return err
	}

	// start IPN watcher to observe state changes
	t.log().Debug("Starting IPN watcher")

	// We start the watcher before entering the StartedState.
	// Since this function holds the mu lock, the watcher won't be able to trigger any state changes until after Start returns,
	// so it's safe to start it here before the state transition.
	watcher, err := NewWatcher(lc, t.reactToIPNStateChange, t.id)
	if err != nil {
		return err
	}

	// Assign all fields before entering StartedState to prevent accessing them before they're set.
	t.server = server
	t.watcher = watcher
	t.socksProxy = socksProxy

	t.setState(StartedState)

	return nil
}

func (t *Tailnet) Stop(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch t.currentState {
	case ConnectedState, StartedState, NeedsLoginState, NeedsMachineAuthState:
		// Allow stopping
	default:
		return fmt.Errorf("unable to stop: tailnet is in state %s", t.currentState)
	}

	// Record user's intent to disable the tailnet.
	t.userState = UserDisabled
	t.notifyUserStateChange(t.userState)

	defer t.setState(StoppedState)

	t.log().Debug("Stopping tailnet")

	server := t.server
	watcher := t.watcher
	socksProxy := t.socksProxy

	if socksProxy != nil {
		t.log().Debug("Stopping SOCKS5 proxy")
		err := socksProxy.Close()
		if err != nil {
			t.log().Error("failed to close SOCKS5 proxy", slog.Any("error", err))
			// Mostly ignoring for now but if the proxy is stuck we get in trouble on start again due to the port being in use.
			return err
		}
		t.log().Debug("SOCKS5 proxy stopped")
	}

	if watcher != nil {
		t.log().Debug("Stopping watcher")
		err := watcher.Close()
		if err != nil {
			t.log().Error("failed to close watcher", slog.Any("error", err))
		}
		t.log().Debug("Watcher stopped")
	}

	if server != nil {
		t.log().Debug("Stopping tsnet server")
		err := server.Close()
		if err != nil {
			t.log().Error("failed to close tsnet server", slog.Any("error", err))
			// TODO: What should we do if the server fails to close? The tailnet is in a bad state either way.
			// Is it stopped, is it started, is it in a failed stop state that is non terminal?
			return err
		}
		t.log().Debug("tsnet server stopped")
	}

	t.log().Debug("Tailnet stopped successfully")

	t.server = nil
	t.watcher = nil
	t.socksProxy = nil

	return nil
}

// Logout logs out from the tailnet and cleans up local state.
// Note: The device may remain visible in the Tailscale admin console as "disconnected"
// until manually deleted or it expires. This is expected Tailscale behavior.
//
// This is different from stop, which just stops the local tsnet server but leaves the machine authenticated with the tailnet.
// After logout, the machine will no longer be able to connect to the tailnet until it's logged in again.
// logout will start the server if it's not already started, then log out from the tailnet.
// During all of this we stay in the LoggingOut state.
// No matter what happens, we transition to the Stopped state at the end, since if logout is successful we're logged out and if logout fails we're in a bad state and stopping is the safest option.
func (t *Tailnet) Logout(ctx context.Context) error {
	t.mu.Lock()

	switch t.currentState {
	case NeedsLoginState:
		// Logout is a no-op in the needs login state since the user is already effectively logged out.
		t.mu.Unlock()
		return nil
	case ConnectedState, StoppedState, StartedState, NeedsMachineAuthState:
		// Allow logout
	default:
		t.mu.Unlock()
		return fmt.Errorf("unable to logout: tailnet is in state %s", t.currentState)
	}

	// Record user's intent to disable the tailnet.
	t.userState = UserDisabled
	t.notifyUserStateChange(t.userState)

	// Set logging out state before releasing the lock so the UI can show it
	// while the network call is in flight.
	t.setState(LoggingOutState)

	// Create server if needed before releasing the lock.
	if t.server == nil {
		t.server = t.newServer(tsnetpkg.TSNetServerConfig{
			Dir:      t.tsnetStateDir,
			Hostname: t.userSetHostname,
			Logf:     t.tsnetLogf(slog.LevelDebug),
			UserLogf: t.tsnetLogf(slog.LevelInfo),
		})
	}
	server := t.server

	t.mu.Unlock()

	// Ensure we transition to stopped state at the end regardless of outcome.
	// We re-acquire the lock here since we released it above to allow the UI
	// to observe LoggingOutState while the network call is in flight.
	defer func() {
		t.mu.Lock()
		t.setState(StoppedState)
		t.mu.Unlock()
	}()

	// Do expensive I/O without holding the lock so LoggingOutState is observable.
	lc, err := server.LocalClient()
	if err != nil {
		t.log().Error("failed to get LocalClient for logout", slog.Any("error", err))
		return err
	}

	t.log().Debug("Logging out from tailnet")

	// TODO: Does logout auto close the server?
	if err := lc.Logout(ctx); err != nil {
		t.log().Error("failed to logout", slog.Any("error", err))
		return err
	}

	t.log().Debug("Successfully logged out from tailnet (device may remain visible in admin console until manually deleted)")
	return nil
}

func (t *Tailnet) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.currentState != ConnectedState {
		return nil, fmt.Errorf("unable to dial: tailnet is in state %s", t.currentState)
	}

	return t.server.Dial(ctx, network, addr)
}

func (t *Tailnet) reactToIPNStateChange(ctx context.Context, ipnState IPNState) {
	t.log().Debug("Reacting to IPN state change", slog.String("ipn_state", ipnState.String()))

	// Hold locks for all the processing but release before notifying to keep lock time in check.
	t.mu.Lock()

	state := t.currentState
	var changed bool
	var terminalCleanup terminalCleanup

	switch state {
	case ConnectedState:
		changed = t.ProcessIPN(ipnState).
			Always(
				t.maybeClaimMagicDNSSuffixLocked,
				t.updatePeersLocked,
				t.maybeUpdateSelfNodeHostnameLocked,
			).
			OneOf(
				t.maybeTransitionToNeedsLoginLocked,
				t.maybeTransitionToNeedsMachineAuthLocked,
			).
			Process()

	case StartedState:
		changed = t.ProcessIPN(ipnState).
			Always(
				t.maybeUpdateSelfNodeHostnameLocked,
			).
			OneOf(
				t.maybeTransitionToNeedsLoginLocked,
				t.maybeTransitionToNeedsMachineAuthLocked,
				t.maybeTransitionToConnectedLocked,
			).
			Process()

	case NeedsLoginState:
		changed = t.ProcessIPN(ipnState).
			Always(
				t.maybeUpdateSelfNodeHostnameLocked,
			).
			OneOf(
				t.maybeTransitionToNeedsMachineAuthLocked,
				t.maybeTransitionToConnectedLocked,
			).
			Process()

	case NeedsMachineAuthState:
		changed = t.ProcessIPN(ipnState).
			Always(
				t.maybeClaimMagicDNSSuffixLocked,
				t.maybeUpdateSelfNodeHostnameLocked,
			).
			OneOf(
				t.maybeTransitionToNeedsLoginLocked,
				t.maybeTransitionToConnectedLocked,
			).
			Process()

	default:
		// Simply ignore IPN state changes in any other state.
	}

	if t.currentState == HasTerminalErrorState {
		terminalCleanup = t.prepareTerminalErrorCleanupLocked()
	}

	t.mu.Unlock()

	terminalCleanup.run(t)

	// Notify after releasing lock to keep lock time minimal.
	if changed {
		t.notify()
	}
}

////
// Internal state management functions
////

// setState updates the current state and notifies listeners.
func (t *Tailnet) setState(state State) {
	t.currentState = state

	t.log().Debug("set state", slog.String("state", string(state)))

	// Notify after releasing the lock to keep lock time minimal.
	t.notify()
}

// notify broadcasts changes that do not require a state transition.
func (t *Tailnet) notify() {
	if t.broadcast != nil {
		t.broadcast()
	}
}

// notifyUserStateChange calls the onUserStateChange callback if set.
// Callers (Start, Stop, Logout) hold t.mu when invoking this; the callback
// only acquires the registry lock, so there is no deadlock risk.
func (t *Tailnet) notifyUserStateChange(s UserState) {
	if t.onUserStateChange != nil {
		t.onUserStateChange(s)
	}
}

// notifyTerminalErrorChange calls the onTerminalErrorChange callback if set.
// Callers hold t.mu when invoking this; the callback only acquires the registry
// lock, so there is no deadlock risk.
func (t *Tailnet) notifyTerminalErrorChange(err string) {
	if t.onTerminalErrorChange != nil {
		t.onTerminalErrorChange(err)
	}
}

func (t *Tailnet) log() *slog.Logger {
	t.logMu.RLock()
	defer t.logMu.RUnlock()
	return t.logger
}

// Locked helpers below: require dataMu to be held by caller.

func (t *Tailnet) maybeClaimMagicDNSSuffixLocked(ipnState IPNState) bool {
	if ipnState.MagicDNSSuffix != "" && t.claimedMagicDNSSuffix != "" && ipnState.MagicDNSSuffix != t.claimedMagicDNSSuffix {
		// TODO: Handle case where the MagicDNS suffix changes while we're running.
		t.log().Error("MagicDNS suffix changed unexpectedly", slog.String("old_suffix", t.claimedMagicDNSSuffix), slog.String("new_suffix", ipnState.MagicDNSSuffix))
		return false
	}

	if ipnState.MagicDNSSuffix == "" || t.claimedMagicDNSSuffix != "" {
		return false
	}

	if err := t.magicDNSSuffixRegistry.Claim(t.id, ipnState.MagicDNSSuffix); err != nil {
		if claimErr, ok := errors.AsType[*AlreadyClaimedError](err); ok {
			t.log().Error("magic DNS suffix claim error", slog.Any("error", claimErr))
			// A duplicate claim is fatal: disable the tailnet and keep it in an
			// unrecoverable error state until the user deletes/recreates it.
			return t.setTerminalErrorLocked(claimErr.Error())
		}

		t.log().Error("failed to claim MagicDNS suffix", slog.String("suffix", ipnState.MagicDNSSuffix), slog.Any("error", err))
		return false
	}

	// Successfully claimed the MagicDNS suffix.
	t.claimedMagicDNSSuffix = ipnState.MagicDNSSuffix

	// Update logger with the new suffix
	t.logMu.Lock()
	t.logger = t.logger.With(slog.String("magic_dns_suffix", ipnState.MagicDNSSuffix))
	t.logMu.Unlock()

	return true
}

func (t *Tailnet) maybeTransitionToNeedsLoginLocked(ipnState IPNState) bool {
	if ipnState.State == nil || *ipnState.State != ipn.NeedsLogin {
		return false
	}

	if ipnState.BrowseToURL == nil || *ipnState.BrowseToURL == "" {
		return false
	}

	t.loginURL = *ipnState.BrowseToURL
	t.currentState = NeedsLoginState
	return true
}

func (t *Tailnet) maybeTransitionToNeedsMachineAuthLocked(ipnState IPNState) bool {
	if ipnState.State == nil || *ipnState.State != ipn.NeedsMachineAuth {
		return false
	}

	t.currentState = NeedsMachineAuthState
	return true
}

func (t *Tailnet) maybeTransitionToConnectedLocked(ipnState IPNState) bool {
	if ipnState.State == nil || *ipnState.State != ipn.Running {
		return false
	}

	t.currentState = ConnectedState
	return true
}

func (t *Tailnet) updatePeersLocked(ipnState IPNState) bool {
	t.peers = ipnState.Peers
	return true
}

func (t *Tailnet) maybeUpdateSelfNodeHostnameLocked(ipnState IPNState) bool {
	if !ipnState.SelfNode.Valid() {
		return false
	}
	hostname := ipnState.SelfNode.ComputedName()
	if hostname == "" || hostname == t.selfNodeHostname {
		return false
	}
	t.selfNodeHostname = hostname
	return true
}

func (t *Tailnet) setTerminalErrorLocked(errMsg string) bool {
	changed := false

	if t.terminalError != errMsg {
		t.terminalError = errMsg
		t.notifyTerminalErrorChange(errMsg)
		changed = true
	}

	if t.userState != UserDisabled {
		t.userState = UserDisabled
		t.notifyUserStateChange(t.userState)
		changed = true
	}

	if t.currentState != HasTerminalErrorState {
		t.currentState = HasTerminalErrorState
		changed = true
	}

	return changed
}

type terminalCleanup struct {
	server     tsnetpkg.TSNetServer
	watcher    *watcher
	socksProxy *socks.Server
}

func (t *Tailnet) prepareTerminalErrorCleanupLocked() terminalCleanup {
	cleanup := terminalCleanup{
		server:     t.server,
		watcher:    t.watcher,
		socksProxy: t.socksProxy,
	}
	t.server = nil
	t.watcher = nil
	t.socksProxy = nil
	return cleanup
}

func (c terminalCleanup) run(t *Tailnet) {
	if c.socksProxy != nil {
		if err := c.socksProxy.Close(); err != nil {
			t.log().Error("failed to close SOCKS5 proxy after terminal error", slog.Any("error", err))
		}
	}

	if c.watcher != nil {
		if err := c.watcher.Close(); err != nil {
			t.log().Error("failed to close watcher after terminal error", slog.Any("error", err))
		}
	}

	if c.server != nil {
		if err := c.server.Close(); err != nil {
			t.log().Error("failed to close tsnet server after terminal error", slog.Any("error", err))
		}
	}
}

func (t *Tailnet) tsnetLogf(level slog.Level) func(string, ...any) {
	return func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		msg = strings.TrimRight(msg, "\n")

		attrs, cleanMsg := parseLogKeyValues(msg)
		if cleanMsg != "" {
			msg = cleanMsg
		}

		allAttrs := make([]any, 0, len(attrs)+1)
		allAttrs = append(allAttrs, slog.String("subcomponent", "tsnet"))
		for _, a := range attrs {
			allAttrs = append(allAttrs, a)
		}

		t.log().Log(context.Background(), level, msg, allAttrs...)
	}
}

func parseLogKeyValues(msg string) ([]slog.Attr, string) {
	var attrs []slog.Attr
	var cleanParts []string

	for _, token := range strings.Fields(msg) {
		key, val, found := strings.Cut(token, "=")
		if !found || key == "" {
			cleanParts = append(cleanParts, token)
			continue
		}

		// Check if key contains only allowed characters
		validKey := true
		for _, r := range key {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.') {
				validKey = false
				break
			}
		}
		if !validKey {
			cleanParts = append(cleanParts, token)
			continue
		}

		attrs = append(attrs, slog.String(key, val))
	}
	return attrs, strings.Join(cleanParts, " ")
}
