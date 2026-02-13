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

	mu               sync.RWMutex
	currentState     State
	connected        State
	hasTerminalError State
	needsLogin       State
	needsMachineAuth State
	started          State
	starting         State
	stopped          State
	stopping         State
	loggingOut       State

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

	t.connected = &ConnectedState{tailnet: t}
	t.hasTerminalError = &HasTerminalErrorState{tailnet: t}
	t.needsLogin = &NeedsLoginState{tailnet: t}
	t.needsMachineAuth = &NeedsMachineAuthState{tailnet: t}
	t.started = &StartedState{tailnet: t}
	t.starting = &StartingState{tailnet: t}
	t.stopped = &StoppedState{tailnet: t}
	t.stopping = &StoppingState{tailnet: t}
	t.loggingOut = &LoggingOutState{tailnet: t}

	if terminalError != "" {
		t.setState(t.hasTerminalError)
	} else {
		t.setState(t.stopped)
	}

	return t
}

// //
// Always available to call
// //
func (t *Tailnet) String() string {
	return fmt.Sprintf("Tailnet{id: %d, state: %s, claimedMagicDNSSuffix: %s, terminalError: %s, socksPort: %d, userSetHostname: %s, peers: %d}", t.id, t.currentState.Name(), t.claimedMagicDNSSuffix, t.terminalError, t.socksPort, t.userSetHostname, len(t.peers))
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
func (t *Tailnet) StateName() StateName {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.currentState.Name()
}

func (t *Tailnet) Start(ctx context.Context) error {
	return t.currentState.Start(ctx)
}

func (t *Tailnet) Stop(ctx context.Context) error {
	return t.currentState.Stop(ctx)
}

// Logout logs out from the tailnet and cleans up local state.
// Note: The device may remain visible in the Tailscale admin console as "disconnected"
// until manually deleted or it expires. This is expected Tailscale behavior.
func (t *Tailnet) Logout(ctx context.Context) error {
	return t.currentState.Logout(ctx)
}

func (t *Tailnet) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return t.currentState.Dial(ctx, network, addr)
}

func (t *Tailnet) Hostname() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	// TODO: Also update this with the hostname retrieved from tailscale itself.
	return t.userSetHostname
}

func (t *Tailnet) LoginURL() (string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.currentState.LoginURL()
}

func (t *Tailnet) Peers() ([]tailcfg.NodeView, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.currentState.Peers()
}

func (t *Tailnet) TerminalError() (string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.currentState.TerminalError()
}

// Not using a log since the functions inside ReactToIPNStateChange themself lock when needed.
func (t *Tailnet) ReactToIPNStateChange(ctx context.Context, ipnState IPNState) error {
	return t.currentState.ReactToIPNStateChange(ctx, ipnState)
}

////
// Internal state management functions that should only be called by the State implementations.
////

func (t *Tailnet) setState(state State) {
	t.mu.Lock()
	t.currentState = state
	t.mu.Unlock()

	t.log().Debug("set state", slog.String("state", string(state.Name())))

	// Notify about the state change after unlocking to prevent holding the lock for a long time.
	if t.broadcast != nil {
		t.broadcast()
	}
}

func (t *Tailnet) setLockedStateNoNotify(state State) {
	t.currentState = state
	t.log().Debug("set state", slog.String("state", string(state.Name())))
}

func (t *Tailnet) log() *slog.Logger {
	t.logMu.RLock()
	defer t.logMu.RUnlock()
	return t.logger
}

func (t *Tailnet) start(ctx context.Context) error {
	t.setState(t.starting)

	t.log().Debug("Starting tailnet")

	t.log().Debug("Starting SOCKS5 proxy", slog.Int("port", t.socksPort))
	socksProxy, err := socks.NewServer(t.Dial, t.socksPort)
	if err != nil {
		t.log().Error("failed to start SOCKS5 proxy", slog.String("error", err.Error()))
		// At this point we haven't started any long-running processes, so we can just return the error without worrying about cleanup.
		// TODO: Give some UI feedback that the server failed to start and the tailnet is non-functional, since the user might not understand why it's auto stopping.
		t.setState(t.stopped)
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
		t.log().Error("failed to start tsnet server", slog.String("error", err.Error()))
		// If we fail to start the server, we should stop the socks proxy that we started since they won't be functional without the server.

		err := t.socksProxy.Close()
		if err != nil {
			t.log().Error("failed to close SOCKS5 proxy after server start failure", slog.String("error", err.Error()))
		}
		t.socksProxy = nil
		// TODO: Give some UI feedback that the server failed to start and the tailnet is non-functional, since the user might not understand why it's auto stopping.
		t.setState(t.stopped)
		return err
	}

	// start IPN watcher to observe state changes
	t.log().Debug("Starting IPN watcher")
	t.watcher = newWatcher(t)
	t.watcher.Start()

	t.setState(t.started)
	return nil
}

func (t *Tailnet) stop(ctx context.Context) error {
	t.setState(t.stopping)

	t.log().Debug("Stopping tailnet")

	if t.socksProxy != nil {
		t.log().Debug("Stopping SOCKS5 proxy")
		err := t.socksProxy.Close()
		if err != nil {
			t.log().Error("failed to close SOCKS5 proxy", slog.String("error", err.Error()))
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
			t.log().Error("failed to close tsnet server", slog.String("error", err.Error()))
			// TODO: What should we do if the server fails to close? The tailnet is in a bad state either way.
			// Is it stopped, is it started, is it in a failed stop state that is non terminal?
			t.setState(t.stopped)
			return err
		}
		t.log().Debug("tsnet server stopped")
		t.server = nil
	}

	t.log().Debug("Tailnet stopped successfully")

	t.setState(t.stopped)
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
	t.setState(t.loggingOut)
	defer t.setState(t.stopped)

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
		t.log().Error("failed to get LocalClient for logout", slog.String("error", err.Error()))
		return err
	}

	t.log().Debug("Logging out from tailnet")

	// TODO: Does logout auto close the server?
	if err := lc.Logout(ctx); err != nil {
		t.log().Error("failed to logout", slog.String("error", err.Error()))
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
			t.log().Error("magic DNS suffix claim error", slog.String("error", claimErr.Error()))
			// This is a terminal error - the tailnet is trying to use a MagicDNS suffix that's already in use
			t.terminalError = claimErr.Error()
			t.setLockedStateNoNotify(t.hasTerminalError)
			t.mu.Unlock()

			// TODO: Persist the terminal error to disk so it survives restarts.

			// Notify about the state change after unlocking to prevent holding the lock for a long time.
			if t.broadcast != nil {
				t.broadcast()
			}
			return
		}

		t.log().Error("failed to claim MagicDNS suffix", slog.String("suffix", ipnState.MagicDNSSuffix), slog.String("error", err.Error()))
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

	t.setLockedStateNoNotify(t.needsLogin)
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

	t.setState(t.needsMachineAuth)
}

func (t *Tailnet) maybeTransitionToConnected(ipnState IPNState) {
	if ipnState.State == nil || *ipnState.State != ipn.Running {
		return
	}

	t.setState(t.connected)
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
