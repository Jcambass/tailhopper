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
	id                    int
	tsnetStateDir         string
	userSetHostname       string
	socksPort             int
	claimedMagicDNSSuffix string
	terminalError         string

	// TODO: Also store hostname from tailscale
	// SelfNode.ComputedName()

	peers    []tailcfg.NodeView
	loginURL string

	mu           sync.RWMutex
	currentState State

	server     *tsnet.Server
	watcher    *watcher
	socksProxy *socks.Server

	magicDNSSuffixRegistry MagicDNSSuffixRegistry
	broadcast              func()

	logMu  sync.RWMutex
	logger *slog.Logger
}

func NewTailnet(id int, tsnetStateDir string, hostname string, claimedMagicDNSSuffix string, terminalError string, socksPort int, magicDNSSuffixRegistry MagicDNSSuffixRegistry, broadcast func()) *Tailnet {
	t := &Tailnet{
		id:                     id,
		tsnetStateDir:          tsnetStateDir,
		userSetHostname:        hostname,
		socksPort:              socksPort,
		claimedMagicDNSSuffix:  claimedMagicDNSSuffix,
		terminalError:          terminalError,
		magicDNSSuffixRegistry: magicDNSSuffixRegistry,
		broadcast:              broadcast,
		logger:                 slog.Default().With("component", "tailnet", "tailnet_id", id),
	}

	if claimedMagicDNSSuffix != "" {
		t.logger = t.logger.With("magic_dns_suffix", claimedMagicDNSSuffix)
	}

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
	return fmt.Sprintf("Tailnet{id: %d, state: %s, claimedMagicDNSSuffix: %s, terminalError: %s, socksPort: %d, userSetHostname: %s, peers: %d}", t.id, t.currentState, t.claimedMagicDNSSuffix, t.terminalError, t.socksPort, t.userSetHostname, len(t.peers))
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
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.claimedMagicDNSSuffix
}

// //
// Based on the current state of the tailnet
// //
func (t *Tailnet) StateName() State {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.currentState
}

func (t *Tailnet) Start(ctx context.Context) error {
	t.mu.RLock()
	state := t.currentState
	t.mu.RUnlock()

	switch state {
	case StoppedState:
		return t.start(ctx)
	case ConnectedState:
		return errors.New("unable to start: tailnet is already connected")
	case StartedState:
		return errors.New("unable to start: tailnet is already started")
	case StartingState:
		return errors.New("unable to start: tailnet is already starting")
	case StoppingState:
		return errors.New("unable to start: tailnet is stopping")
	case NeedsLoginState:
		return errors.New("unable to start: tailnet is already started (needs login)")
	case NeedsMachineAuthState:
		return errors.New("unable to start: tailnet is already started (needs machine auth)")
	case HasTerminalErrorState:
		return errors.New("unable to start: tailnet is already started (has terminal error)")
	case LoggingOutState:
		return errors.New("unable to start: tailnet is logging out")
	default:
		return errors.New("unable to start: unknown state")
	}
}

func (t *Tailnet) Stop(ctx context.Context) error {
	t.mu.RLock()
	state := t.currentState
	t.mu.RUnlock()

	switch state {
	case StoppedState:
		return errors.New("unable to stop: tailnet is already stopped")
	case StoppingState:
		return errors.New("unable to stop: tailnet is already stopping")
	case LoggingOutState:
		return errors.New("unable to stop: tailnet is logging out")
	case ConnectedState, StartedState, StartingState, NeedsLoginState, NeedsMachineAuthState, HasTerminalErrorState:
		return t.stop(ctx)
	default:
		return errors.New("unable to stop: unknown state")
	}
}

// Logout logs out from the tailnet and cleans up local state.
// Note: The device may remain visible in the Tailscale admin console as "disconnected"
// until manually deleted or it expires. This is expected Tailscale behavior.
func (t *Tailnet) Logout(ctx context.Context) error {
	t.mu.RLock()
	state := t.currentState
	t.mu.RUnlock()

	switch state {
	case NeedsLoginState:
		// Logout is a no-op in the needs login state since the user is already effectively logged out.
		return nil
	case StartingState:
		return errors.New("unable to logout: tailnet is starting")
	case StoppingState:
		return errors.New("unable to logout: tailnet is stopping")
	case LoggingOutState:
		return errors.New("unable to logout: tailnet is already logging out")
	case ConnectedState, StoppedState, StartedState, NeedsMachineAuthState, HasTerminalErrorState:
		return t.logout(ctx)
	default:
		return errors.New("unable to logout: unknown state")
	}
}

func (t *Tailnet) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	t.mu.RLock()
	state := t.currentState
	server := t.server
	t.mu.RUnlock()

	switch state {
	case ConnectedState:
		return server.Dial(ctx, network, addr)
	case StoppedState:
		return nil, errors.New("unable to dial: tailnet is stopped")
	case StartedState:
		return nil, errors.New("unable to dial: tailnet is started but not connected yet")
	case StartingState:
		return nil, errors.New("unable to dial: tailnet is starting")
	case StoppingState:
		return nil, errors.New("unable to dial: tailnet is stopping")
	case NeedsLoginState:
		return nil, errors.New("unable to dial: tailnet needs login")
	case NeedsMachineAuthState:
		return nil, errors.New("unable to dial: tailnet needs machine auth")
	case HasTerminalErrorState:
		return nil, errors.New("unable to dial: tailnet has terminal error")
	case LoggingOutState:
		return nil, errors.New("unable to dial: tailnet is logging out")
	default:
		return nil, errors.New("unable to dial: unknown state")
	}
}

func (t *Tailnet) Hostname() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	// TODO: Also update this with the hostname retrieved from tailscale itself.
	return t.userSetHostname
}

func (t *Tailnet) LoginURL() (string, error) {
	t.mu.RLock()
	state := t.currentState
	loginURL := t.loginURL
	t.mu.RUnlock()

	switch state {
	case NeedsLoginState:
		return loginURL, nil
	case ConnectedState:
		return "", errors.New("unable to get login URL: tailnet is connected")
	case StoppedState:
		return "", errors.New("unable to get login URL: tailnet is stopped")
	case StartedState:
		return "", errors.New("unable to get login URL: tailnet is started but not connected yet")
	case StartingState:
		return "", errors.New("unable to get login URL: tailnet is starting")
	case StoppingState:
		return "", errors.New("unable to get login URL: tailnet is stopping")
	case NeedsMachineAuthState:
		return "", errors.New("unable to get login URL: tailnet needs machine auth")
	case HasTerminalErrorState:
		return "", errors.New("unable to get login URL: tailnet has terminal error")
	case LoggingOutState:
		return "", errors.New("unable to get login URL: tailnet is logging out")
	default:
		return "", errors.New("unable to get login URL: unknown state")
	}
}

func (t *Tailnet) Peers() ([]tailcfg.NodeView, error) {
	t.mu.RLock()
	state := t.currentState
	peers := t.peers
	t.mu.RUnlock()

	switch state {
	case ConnectedState:
		return peers, nil
	case StoppedState:
		return nil, errors.New("unable to get peers: tailnet is stopped")
	case StartedState:
		return nil, errors.New("unable to get peers: tailnet is started but not connected yet")
	case StartingState:
		return nil, errors.New("unable to get peers: tailnet is starting")
	case StoppingState:
		return nil, errors.New("unable to get peers: tailnet is stopping")
	case NeedsLoginState:
		return nil, errors.New("unable to get peers: tailnet needs login")
	case NeedsMachineAuthState:
		return nil, errors.New("unable to get peers: tailnet needs machine auth")
	case HasTerminalErrorState:
		return nil, errors.New("unable to get peers: tailnet has terminal error")
	case LoggingOutState:
		return nil, errors.New("unable to get peers: tailnet is logging out")
	default:
		return nil, errors.New("unable to get peers: unknown state")
	}
}

func (t *Tailnet) TerminalError() (string, error) {
	t.mu.RLock()
	state := t.currentState
	terminalError := t.terminalError
	t.mu.RUnlock()

	switch state {
	case HasTerminalErrorState:
		return terminalError, nil
	case ConnectedState:
		return "", errors.New("unable to get terminal error: tailnet is connected")
	case StoppedState:
		return "", errors.New("unable to get terminal error: tailnet is stopped")
	case StartedState:
		return "", errors.New("unable to get terminal error: tailnet is started but not connected yet")
	case StartingState:
		return "", errors.New("unable to get terminal error: tailnet is starting")
	case StoppingState:
		return "", errors.New("unable to get terminal error: tailnet is stopping")
	case NeedsLoginState:
		return "", errors.New("unable to get terminal error: tailnet needs login")
	case NeedsMachineAuthState:
		return "", errors.New("unable to get terminal error: tailnet needs machine auth")
	case LoggingOutState:
		return "", errors.New("unable to get terminal error: tailnet is logging out")
	default:
		return "", errors.New("unable to get terminal error: unknown state")
	}
}

// Not using a log since the functions inside ReactToIPNStateChange themself lock when needed.
func (t *Tailnet) ReactToIPNStateChange(ctx context.Context, ipnState IPNState) error {
	t.mu.RLock()
	state := t.currentState
	t.mu.RUnlock()

	switch state {
	case ConnectedState:
		t.maybeClaimMagicDNSSuffix(ipnState)
		t.maybeTransitionToNeedsLogin(ipnState)
		t.maybeTransitionToNeedsMachineAuth(ipnState)
		t.maybeUpdatePeers(ipnState)
		return nil
	case StartedState:
		t.maybeTransitionToNeedsLogin(ipnState)
		t.maybeTransitionToNeedsMachineAuth(ipnState)
		t.maybeTransitionToConnected(ipnState)
		return nil
	case NeedsLoginState:
		t.maybeTransitionToNeedsMachineAuth(ipnState)
		t.maybeTransitionToConnected(ipnState)
		return nil
	case NeedsMachineAuthState:
		t.maybeClaimMagicDNSSuffix(ipnState)
		t.maybeTransitionToNeedsLogin(ipnState)
		t.maybeTransitionToConnected(ipnState)
		return nil
	case StoppedState, StartingState, StoppingState, HasTerminalErrorState, LoggingOutState:
		// Simply ignore IPN state changes in these states.
		return nil
	default:
		return errors.New("unable to react to IPN state change: unknown state")
	}
}

////
// Internal state management functions that should only be called by the State implementations.
////

func (t *Tailnet) setState(state State) {
	t.mu.Lock()
	t.currentState = state
	t.mu.Unlock()

	t.log().Debug("set state", slog.String("state", string(state)))

	// Notify about the state change after unlocking to prevent holding the lock for a long time.
	if t.broadcast != nil {
		t.broadcast()
	}
}

func (t *Tailnet) setLockedStateNoNotify(state State) {
	t.currentState = state
	t.log().Debug("set state", slog.String("state", string(state)))
}

func (t *Tailnet) log() *slog.Logger {
	t.logMu.RLock()
	defer t.logMu.RUnlock()
	return t.logger
}

func (t *Tailnet) start(ctx context.Context) error {
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
	t.socksProxy = socksProxy
	t.socksProxy.Start()

	// Asynchronously start the server
	t.log().Debug("Starting tsnet server")

	t.server = &tsnet.Server{
		Dir:      t.tsnetStateDir,
		Hostname: t.userSetHostname,
		UserLogf: t.tsnetLogf(slog.LevelInfo),
		Logf:     t.tsnetLogf(slog.LevelDebug),
	}

	err = t.server.Start()
	if err != nil {
		t.log().Error("failed to start tsnet server", slog.Any("error", err))
		// If we fail to start the server, we should stop the socks proxy that we started since they won't be functional without the server.

		err := t.socksProxy.Close()
		if err != nil {
			t.log().Error("failed to close SOCKS5 proxy after server start failure", slog.Any("error", err))
		}
		t.socksProxy = nil
		// TODO: Give some UI feedback that the server failed to start and the tailnet is non-functional, since the user might not understand why it's auto stopping.
		t.setState(StoppedState)
		return err
	}

	// start IPN watcher to observe state changes
	t.log().Debug("Starting IPN watcher")
	t.watcher = newWatcher(t)
	t.watcher.Start()

	t.setState(StartedState)
	return nil
}

func (t *Tailnet) stop(ctx context.Context) error {
	t.setState(StoppingState)

	t.log().Debug("Stopping tailnet")

	if t.socksProxy != nil {
		t.log().Debug("Stopping SOCKS5 proxy")
		err := t.socksProxy.Close()
		if err != nil {
			t.log().Error("failed to close SOCKS5 proxy", slog.Any("error", err))
			// Mostly ignoring for now but if the proxy is stuck we get in trouble on start again due to the port being in use.
			return err
		}
		t.socksProxy = nil
		t.log().Debug("SOCKS5 proxy stopped")
	}

	if t.watcher != nil {
		t.log().Debug("Stopping watcher")
		t.watcher.Stop()
		t.watcher = nil
		t.log().Debug("Watcher stopped")
	}

	if t.server != nil {
		t.log().Debug("Stopping tsnet server")
		err := t.server.Close()
		if err != nil {
			t.log().Error("failed to close tsnet server", slog.Any("error", err))
			// TODO: What should we do if the server fails to close? The tailnet is in a bad state either way.
			// Is it stopped, is it started, is it in a failed stop state that is non terminal?
			t.setState(StoppedState)
			return err
		}
		t.log().Debug("tsnet server stopped")
		t.server = nil
	}

	t.log().Debug("Tailnet stopped successfully")

	t.setState(StoppedState)
	return nil
}

// logout logs the machine out of the tailnet. This is different from stop, which just stops the local tsnet server but leaves the machine authenticated with the tailnet.
// After logout, the machine will no longer be able to connect to the tailnet until it's logged in again.
// logout will start the server if it's not already started, then log out from the tailnet.
// During all of this we stay in the LoggingOut state.
// No matter what happens, we transition to the Stopped state at the end, since if logout is successful we're logged out and if logout fails we're in a bad state and stopping is the safest option.
func (t *Tailnet) logout(ctx context.Context) error {
	// TODO: This and start/stop need some concurrency protection.
	// State changes themself are guarded but I think we can still mess up and it's hard to see what's safe and what is not!
	t.setState(LoggingOutState)
	defer t.setState(StoppedState)

	if t.server == nil {
		t.server = &tsnet.Server{
			Dir:      t.tsnetStateDir,
			Hostname: t.userSetHostname,
			UserLogf: t.tsnetLogf(slog.LevelInfo),
			Logf:     t.tsnetLogf(slog.LevelDebug),
		}
	}

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

func (t *Tailnet) maybeClaimMagicDNSSuffix(ipnState IPNState) {
	// Using the tailnet wide mu for now.
	t.mu.Lock()

	if t.claimedMagicDNSSuffix != "" {
		// We have already claimed a MagicDNS suffix.
		t.mu.Unlock()
		return
	}

	// TODO: Handle case where the MagicDNS suffix changes while we're running.

	if ipnState.MagicDNSSuffix == "" {
		// No MagicDNS suffix to claim.
		t.mu.Unlock()
		return
	}

	if err := t.magicDNSSuffixRegistry.Claim(t.id, ipnState.MagicDNSSuffix); err != nil {
		if claimErr, ok := errors.AsType[*AlreadyClaimedError](err); ok {
			t.log().Error("magic DNS suffix claim error", slog.Any("error", claimErr))
			// This is a terminal error - the tailnet is trying to use a MagicDNS suffix that's already in use
			t.terminalError = claimErr.Error()
			t.setLockedStateNoNotify(HasTerminalErrorState)
			t.mu.Unlock()

			// TODO: Persist the terminal error to disk so it survives restarts.

			// Notify about the state change after unlocking to prevent holding the lock for a long time.
			if t.broadcast != nil {
				t.broadcast()
			}
			return
		}

		t.log().Error("failed to claim MagicDNS suffix", slog.String("suffix", ipnState.MagicDNSSuffix), slog.Any("error", err))
		return
	}

	// Successfully claimed the MagicDNS suffix. Update our state and notify about the change.
	t.claimedMagicDNSSuffix = ipnState.MagicDNSSuffix

	// Update logger with the new suffix
	t.logMu.Lock()
	t.logger = t.logger.With("magic_dns_suffix", ipnState.MagicDNSSuffix)
	t.logMu.Unlock()

	// Unlock before notifying about the state change to prevent holding the lock for a long time.
	// Ideally we can redesign the state notifier to be async.
	t.mu.Unlock()

	// Notify about the state change
	if t.broadcast != nil {
		t.broadcast()
	}
}

func (t *Tailnet) maybeTransitionToNeedsLogin(ipnState IPNState) {
	// Using the tailnet wide mu for now.
	t.mu.Lock()

	if ipnState.State == nil || *ipnState.State != ipn.NeedsLogin {
		t.mu.Unlock()
		return
	}

	if ipnState.BrowseToURL == nil || *ipnState.BrowseToURL == "" {
		t.mu.Unlock()
		return
	}

	t.loginURL = *ipnState.BrowseToURL

	t.setLockedStateNoNotify(NeedsLoginState)
	t.mu.Unlock()

	// Notify about the state change after unlocking to prevent holding the lock for a long time.
	if t.broadcast != nil {
		t.broadcast()
	}
}

func (t *Tailnet) maybeTransitionToNeedsMachineAuth(ipnState IPNState) {
	if ipnState.State == nil || *ipnState.State != ipn.NeedsMachineAuth {
		return
	}

	t.setState(NeedsMachineAuthState)
}

func (t *Tailnet) maybeTransitionToConnected(ipnState IPNState) {
	if ipnState.State == nil || *ipnState.State != ipn.Running {
		return
	}

	t.setState(ConnectedState)
}

func (t *Tailnet) maybeUpdatePeers(ipnState IPNState) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.peers = ipnState.Peers
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
