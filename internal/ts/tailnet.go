package ts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"

	"github.com/jcambass/tailhopper/internal/socks"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
)

type Tailnet struct {
	// Immutable after construction.
	id                     int
	socksPort              int
	tsnetStateDir          string
	userSetHostname        string
	magicDNSSuffixRegistry MagicDNSSuffixRegistry
	broadcast              func()

	// Logger has its own lock to avoid blocking state reads.
	logMu  sync.RWMutex
	logger *slog.Logger

	// TODO: We now block the UI fully during state transitions.
	// This makes the distinction between StoppingState and StoppedState useless since the UI will just hang.
	mu sync.RWMutex

	// Only changed by commands (start, stop, logout).
	server     *tsnet.Server
	watcher    *watcher
	socksProxy *socks.Server

	// Also changed by IPN state changes.
	claimedMagicDNSSuffix string
	terminalError         string
	peers                 []tailcfg.NodeView
	loginURL              string
	currentState          State

	// TODO: Also store hostname from tailscale
	// SelfNode.ComputedName()
}

type TailnetSnapshot struct {
	ID             int
	State          State
	MagicDNSSuffix string
	Hostname       string
	LoginURL       string
	Peers          []tailcfg.NodeView
	TerminalError  string
}

func (s *TailnetSnapshot) String() string {
	return fmt.Sprintf("TailnetSnapshot{ID: %d, State: %s, MagicDNSSuffix: %s, Hostname: %s, LoginURL: %s, Peers: %d, TerminalError: %s}", s.ID, s.State, s.MagicDNSSuffix, s.Hostname, s.LoginURL, len(s.Peers), s.TerminalError)
}

func NewTailnet(id int, tsnetStateDir string, hostname string, claimedMagicDNSSuffix string, terminalError string, socksPort int, magicDNSSuffixRegistry MagicDNSSuffixRegistry, broadcast func()) *Tailnet {
	t := &Tailnet{
		id:                     id,
		magicDNSSuffixRegistry: magicDNSSuffixRegistry,
		broadcast:              broadcast,
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
	} else {
		t.currentState = StoppedState
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

	return TailnetSnapshot{
		ID:             t.id,
		MagicDNSSuffix: t.claimedMagicDNSSuffix,
		// TODO: Also update this with the hostname retrieved from tailscale itself.
		Hostname:      t.userSetHostname,
		LoginURL:      t.loginURL,
		Peers:         t.peers,
		TerminalError: t.terminalError,
		State:         t.currentState,
	}
}

// Commands
func (t *Tailnet) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.currentState != StoppedState {
		return fmt.Errorf("unable to start: tailnet is in state %s", t.currentState)
	}

	// Set starting state
	t.setState(StartingState)

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

	server := &tsnet.Server{
		Dir:      t.tsnetStateDir,
		Hostname: t.userSetHostname,
		UserLogf: t.tsnetLogf(slog.LevelInfo),
		Logf:     t.tsnetLogf(slog.LevelDebug),
	}

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
	watcher := newWatcher(lc, t.reactToIPNStateChange, t.id)

	t.server = server
	t.watcher = watcher
	t.socksProxy = socksProxy
	// Update state before starting the watcher since the watcher may trigger state changes immediately on start and we want to be in the correct state to handle those.
	// The watcher will wait for the commandMu to be released before processing any IPN state changes, but this order makes things clearer.
	t.setState(StartedState)

	watcher.Start()

	return nil
}

func (t *Tailnet) Stop(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch t.currentState {
	case ConnectedState, StartedState, StartingState, NeedsLoginState, NeedsMachineAuthState, HasTerminalErrorState:
		// Allow stopping
	default:
		return fmt.Errorf("unable to stop: tailnet is in state %s", t.currentState)
	}

	// Set stopping state
	t.setState(StoppingState)
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
		watcher.Stop()
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
	defer t.mu.Unlock()

	switch t.currentState {
	case NeedsLoginState:
		// Logout is a no-op in the needs login state since the user is already effectively logged out.
		return nil
	case ConnectedState, StoppedState, StartedState, NeedsMachineAuthState, HasTerminalErrorState:
		// Allow logout
	default:
		return fmt.Errorf("unable to logout: tailnet is in state %s", t.currentState)
	}

	// Set logging out state
	t.setState(LoggingOutState)

	// Ensure we transition to stopped state at the end
	defer t.setState(StoppedState)

	// Create server if needed (outside dataMu)
	if t.server == nil {
		t.server = &tsnet.Server{
			Dir:      t.tsnetStateDir,
			Hostname: t.userSetHostname,
			UserLogf: t.tsnetLogf(slog.LevelInfo),
			Logf:     t.tsnetLogf(slog.LevelDebug),
		}
	}

	// Do expensive I/O without holding dataMu (only opMu is held)
	lc, err := t.server.LocalClient()
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

	switch state {
	case ConnectedState:
		changed = t.ProcessIPN(ipnState).
			Always(
				t.maybeClaimMagicDNSSuffixLocked,
				t.updatePeersLocked,
			).
			OneOf(
				t.maybeTransitionToNeedsLoginLocked,
				t.maybeTransitionToNeedsMachineAuthLocked,
			).
			Process()

	case StartedState:
		changed = t.ProcessIPN(ipnState).
			OneOf(
				t.maybeTransitionToNeedsLoginLocked,
				t.maybeTransitionToNeedsMachineAuthLocked,
				t.maybeTransitionToConnectedLocked,
			).
			Process()

	case NeedsLoginState:
		changed = t.ProcessIPN(ipnState).
			OneOf(
				t.maybeTransitionToNeedsMachineAuthLocked,
				t.maybeTransitionToConnectedLocked,
			).
			Process()

	case NeedsMachineAuthState:
		changed = t.ProcessIPN(ipnState).
			Always(t.maybeClaimMagicDNSSuffixLocked).
			OneOf(
				t.maybeTransitionToNeedsLoginLocked,
				t.maybeTransitionToConnectedLocked,
			).
			Process()

	default:
		// Simply ignore IPN state changes in any other state.
	}

	t.mu.Unlock()

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

func (t *Tailnet) log() *slog.Logger {
	t.logMu.RLock()
	defer t.logMu.RUnlock()
	return t.logger
}

// Locked helpers below: require dataMu to be held by caller.

func (t *Tailnet) maybeClaimMagicDNSSuffixLocked(ipnState IPNState) bool {
	// TODO: Handle case where the MagicDNS suffix changes while we're running.
	if ipnState.MagicDNSSuffix == "" || t.claimedMagicDNSSuffix != "" {
		return false
	}

	if err := t.magicDNSSuffixRegistry.Claim(t.id, ipnState.MagicDNSSuffix); err != nil {
		if claimErr, ok := errors.AsType[*AlreadyClaimedError](err); ok {
			t.log().Error("magic DNS suffix claim error", slog.Any("error", claimErr))
			// This is a terminal error - the tailnet is trying to use a MagicDNS suffix that's already in use
			t.terminalError = claimErr.Error()
			t.currentState = HasTerminalErrorState
			return true
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
