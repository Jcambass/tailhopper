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

	// We use two separate locks, commandMu and dataMu.
	// 1. Executing commands requires holding the commandMu write lock, which ensures that only one command can be executing at a time.
	// 2. Executing state or status updates requires holding a Read Lock on commandMu (to ensure no commands are executing) and a write lock on dataMu (to ensure the state is not changing while we're reading it).
	// 3. Reading state or status requires holding a Read Lock on dataMu, but does not require holding commandMu. This does mean that a command might be in-flight and state fields might not be consistent with each other!
	//    This is a tradeoff to allow the UI to read state without being blocked by commands, but it means that the UI needs to be resilient to potentially inconsistent state.
	//    If the code does rely on consistency between fields, it should acquire the commandMu read lock as well to ensure that no commands are in-flight that might be changing the state.
	//    We might revisit this decision in the future if it proves to be too difficult to work with, but for now it allows for more responsive UI updates without being blocked by long-running commands.

	// commandMu protects user operations (Start, Stop, Logout) from running concurrently.
	// We use an RWMutex so that status checks can proceed concurrently, but commands are exclusive.
	// Lock order: commandMu -> dataMu
	commandMu sync.RWMutex

	// dataMu protects all mutable state fields that are relevant to the current state of the tailnet.
	// Lock order: commandMu -> dataMu
	dataMu sync.RWMutex

	// Protected by commandMu.
	server     *tsnet.Server
	watcher    *watcher
	socksProxy *socks.Server

	// Protected by dataMu.
	claimedMagicDNSSuffix string
	terminalError         string
	peers                 []tailcfg.NodeView
	loginURL              string
	currentState          State

	// TODO: Also store hostname from tailscale
	// SelfNode.ComputedName()
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

	// TODO: Set state directly without lock and notify.
	if terminalError != "" {
		t.setState(HasTerminalErrorState)
	} else {
		t.setState(StoppedState)
	}

	return t
}

// //
// Always available to call
// //
func (t *Tailnet) String() string {
	t.dataMu.RLock()
	defer t.dataMu.RUnlock()

	state := t.currentState
	claimedMagicDNSSuffix := t.claimedMagicDNSSuffix
	terminalError := t.terminalError
	peers := t.peers

	return fmt.Sprintf("Tailnet{id: %d, state: %s, claimedMagicDNSSuffix: %s, terminalError: %s, socksPort: %d, userSetHostname: %s, peers: %d}", t.id, state, claimedMagicDNSSuffix, terminalError, t.socksPort, t.userSetHostname, len(peers))
}

func (t *Tailnet) ID() int {
	return t.id
}

func (t *Tailnet) SocksAddr() string {
	return fmt.Sprintf("localhost:%d", t.socksPort)
}

// //
// Indirectly based on the current state of the tailnet but always callable.
// //
func (t *Tailnet) MagicDNSSuffix() string {
	t.dataMu.RLock()
	defer t.dataMu.RUnlock()

	return t.claimedMagicDNSSuffix
}

// //
// Based on the current state of the tailnet
// //
func (t *Tailnet) StateName() State {
	t.dataMu.RLock()
	defer t.dataMu.RUnlock()

	return t.currentState
}

func (t *Tailnet) Start(ctx context.Context) error {
	t.commandMu.Lock()
	defer t.commandMu.Unlock()

	// Since the watcher is not running now and other commands are guarded by the commandMu, it's ok to read the state, release the dataMu and only acquire it again when we need to update state.
	t.dataMu.RLock()
	state := t.currentState
	t.dataMu.RUnlock()

	if state != StoppedState {
		return fmt.Errorf("unable to start: tailnet is in state %s", state)
	}

	// Set starting state
	t.setState(StartingState)

	t.log().Debug("Starting tailnet")

	// Do expensive I/O without holding dataMu
	// Snapshot mutable fields under lock.
	stateDir := t.tsnetStateDir
	hostname := t.userSetHostname

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
		Dir:      stateDir,
		Hostname: hostname,
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
	t.commandMu.Lock()
	defer t.commandMu.Unlock()

	t.dataMu.RLock()
	state := t.currentState
	t.dataMu.RUnlock()

	switch state {
	case ConnectedState, StartedState, StartingState, NeedsLoginState, NeedsMachineAuthState, HasTerminalErrorState:
		// Allow stopping
	default:
		return fmt.Errorf("unable to stop: tailnet is in state %s", state)
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
	t.commandMu.Lock()
	defer t.commandMu.Unlock()

	t.dataMu.RLock()
	state := t.currentState
	t.dataMu.RUnlock()

	switch state {
	case NeedsLoginState:
		// Logout is a no-op in the needs login state since the user is already effectively logged out.
		return nil
	case ConnectedState, StoppedState, StartedState, NeedsMachineAuthState, HasTerminalErrorState:
		// Allow logout
	default:
		return fmt.Errorf("unable to logout: tailnet is in state %s", state)
	}

	// Set logging out state
	t.setState(LoggingOutState)

	// Ensure we transition to stopped state at the end
	defer t.setState(StoppedState)

	// Read server under commandMu
	server := t.server
	stateDir := t.tsnetStateDir
	hostname := t.userSetHostname

	// Create server if needed (outside dataMu)
	if server == nil {
		server = &tsnet.Server{
			Dir:      stateDir,
			Hostname: hostname,
			UserLogf: t.tsnetLogf(slog.LevelInfo),
			Logf:     t.tsnetLogf(slog.LevelDebug),
		}
	}

	// Do expensive I/O without holding dataMu (only opMu is held)
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
	t.commandMu.RLock()
	defer t.commandMu.RUnlock()

	server := t.server

	t.dataMu.RLock()
	state := t.currentState
	t.dataMu.RUnlock()

	if state != ConnectedState {
		return nil, fmt.Errorf("unable to dial: tailnet is in state %s", state)
	}

	return server.Dial(ctx, network, addr)
}

func (t *Tailnet) Hostname() string {
	// TODO: Also update this with the hostname retrieved from tailscale itself.
	return t.userSetHostname
}

func (t *Tailnet) LoginURL() (string, error) {
	t.dataMu.RLock()
	state := t.currentState
	loginURL := t.loginURL
	t.dataMu.RUnlock()

	if state != NeedsLoginState {
		return "", fmt.Errorf("unable to get login URL: tailnet is in state %s", state)
	}

	return loginURL, nil
}

func (t *Tailnet) Peers() ([]tailcfg.NodeView, error) {
	t.dataMu.RLock()
	defer t.dataMu.RUnlock()

	if t.currentState != ConnectedState {
		return nil, fmt.Errorf("unable to get peers: tailnet is in state %s", t.currentState)
	}

	return t.peers, nil
}

func (t *Tailnet) TerminalError() (string, error) {
	t.commandMu.RLock()
	defer t.commandMu.RUnlock()

	t.dataMu.RLock()
	state := t.currentState
	terminalError := t.terminalError
	t.dataMu.RUnlock()

	if state != HasTerminalErrorState {
		return "", fmt.Errorf("unable to get terminal error: tailnet is in state %s", state)
	}

	return terminalError, nil
}

func (t *Tailnet) reactToIPNStateChange(ctx context.Context, ipnState IPNState) {
	t.log().Debug("Reacting to IPN state change", slog.String("ipn_state", ipnState.String()))

	// Hold locks for all the processing but release before notifying to keep lock time in check.
	t.commandMu.RLock()
	t.dataMu.Lock()

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

	t.dataMu.Unlock()
	t.commandMu.RUnlock()

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
	t.dataMu.Lock()
	t.currentState = state
	t.dataMu.Unlock()

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
