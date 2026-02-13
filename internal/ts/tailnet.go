package ts

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/jcambass/tailhopper/internal/logging"
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

	logger *logging.Logger

	server     *tsnet.Server
	watcher    *watcher
	socksProxy *socks.Server

	magicDNSSuffixRegistry MagicDNSSuffixRegistry
	broadcast              func()
}

func NewTailnet(id int, tsnetStateDir string, hostname string, claimedMagicDNSSuffix string, terminalError string, socksPort int, logger *logging.Logger, magicDNSSuffixRegistry MagicDNSSuffixRegistry, broadcast func()) *Tailnet {
	if logger == nil {
		logger = logging.Default().With("component", "tailnet")
	}

	t := &Tailnet{
		id:                     id,
		tsnetStateDir:          tsnetStateDir,
		userSetHostname:        hostname,
		socksPort:              socksPort,
		claimedMagicDNSSuffix:  claimedMagicDNSSuffix,
		terminalError:          terminalError,
		logger:                 logger,
		magicDNSSuffixRegistry: magicDNSSuffixRegistry,
		broadcast:              broadcast,
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

func (t *Tailnet) String() string {
	return fmt.Sprintf("Tailnet{id: %d, state: %s, claimedMagicDNSSuffix: %s, terminalError: %s, socksPort: %d, userSetHostname: %s, peers: %d}", t.id, t.currentState.Name(), t.claimedMagicDNSSuffix, t.terminalError, t.socksPort, t.userSetHostname, len(t.peers))
}

func (t *Tailnet) setState(state State) {
	t.mu.Lock()
	t.currentState = state
	t.mu.Unlock()

	t.logger.Printf("set state to %s", string(state.Name()))

	// Notify about the state change after unlocking to prevent holding the lock for a long time.
	if t.broadcast != nil {
		t.broadcast()
	}
}

func (t *Tailnet) setLockedStateNoNotify(state State) {
	t.currentState = state
	t.logger.Printf("set state to %s", string(state.Name()))
}

func (t *Tailnet) ID() int {
	return t.id
}

func (t *Tailnet) start(ctx context.Context) error {
	t.setState(t.starting)

	t.logger.Printf("Starting tailnet")

	t.logger.Printf("Starting SOCKS5 proxy on port %d", t.socksPort)
	socksProxy, err := socks.NewServer(t.Dial, t.socksPort)
	if err != nil {
		t.logger.Printf("failed to start SOCKS5 proxy: %v", err)
		// At this point we haven't started any long-running processes, so we can just return the error without worrying about cleanup.
		// TODO: Give some UI feedback that the server failed to start and the tailnet is non-functional, since the user might not understand why it's auto stopping.
		t.setState(t.stopped)
		return err
	}
	t.socksProxy = socksProxy
	t.socksProxy.Start()

	// Asynchronously start the server
	t.logger.Printf("Starting tsnet server")

	t.server = &tsnet.Server{
		Dir:      t.tsnetStateDir,
		Hostname: t.userSetHostname,
	}

	err = t.server.Start()
	if err != nil {
		t.logger.Printf("failed to start tsnet server: %v", err)
		// If we fail to start the server, we should stop the socks proxy that we started since they won't be functional without the server.

		err := t.socksProxy.Close()
		if err != nil {
			t.logger.Printf("failed to close SOCKS5 proxy after server start failure: %v", err)
		}
		t.socksProxy = nil
		// TODO: Give some UI feedback that the server failed to start and the tailnet is non-functional, since the user might not understand why it's auto stopping.
		t.setState(t.stopped)
		return err
	}

	// start IPN watcher to observe state changes
	t.logger.Printf("Starting IPN watcher")
	t.watcher = newWatcher(t)
	t.watcher.Start()

	t.setState(t.started)
	return nil
}

func (t *Tailnet) Start(ctx context.Context) error {
	return t.currentState.Start(ctx)
}

func (t *Tailnet) stop(ctx context.Context) error {
	t.setState(t.stopping)

	t.logger.Printf("Stopping tailnet")

	if t.socksProxy != nil {
		t.logger.Printf("Stopping SOCKS5 proxy")
		err := t.socksProxy.Close()
		if err != nil {
			t.logger.Printf("failed to close SOCKS5 proxy: %v", err)
			// Mostly ignoring for now but if the proxy is stuck we get in trouble on start again due to the port being in use.
			return err
		}
		t.socksProxy = nil
		t.logger.Printf("SOCKS5 proxy stopped")
	}

	if t.watcher != nil {
		t.logger.Printf("Stopping watcher")
		t.watcher.Stop()
		t.watcher = nil
		t.logger.Printf("Watcher stopped")
	}

	if t.server != nil {
		t.logger.Printf("Stopping tsnet server")
		err := t.server.Close()
		if err != nil {
			t.logger.Printf("failed to close tsnet server: %v", err)
			// TODO: What should we do if the server fails to close? The tailnet is in a bad state either way.
			// Is it stopped, is it started, is it in a failed stop state that is non terminal?
			t.setState(t.stopped)
			return err
		}
		t.logger.Printf("tsnet server stopped")
		t.server = nil
	}

	t.logger.Printf("Tailnet stopped successfully")

	t.setState(t.stopped)
	return nil
}

func (t *Tailnet) Stop(ctx context.Context) error {
	return t.currentState.Stop(ctx)
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
		}
	}

	lc, err := t.server.LocalClient()
	if err != nil {
		t.logger.Printf("failed to get LocalClient for logout: %v", err)
		return err
	}

	t.logger.Printf("Logging out from tailnet")

	// TODO: Does logout auto close the server?
	if err := lc.Logout(ctx); err != nil {
		t.logger.Printf("failed to logout: %v", err)
		return err
	}

	t.logger.Printf("Successfully logged out from tailnet (device may remain visible in admin console until manually deleted)")
	return nil
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

func (t *Tailnet) SocksAddr() string {
	return fmt.Sprintf("localhost:%d", t.socksPort)
}

func (t *Tailnet) MagicDNSSuffix() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.claimedMagicDNSSuffix
}

func (t *Tailnet) StateName() StateName {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.currentState.Name()
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

func (t *Tailnet) ReactToIPNStateChange(ctx context.Context, ipnState IPNState) error {
	return t.currentState.ReactToIPNStateChange(ctx, ipnState)
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
			t.logger.Println(claimErr)
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

		t.logger.Printf("failed to claim MagicDNS suffix %s: %v", ipnState.MagicDNSSuffix, err)
		return
	}

	// Successfully claimed the MagicDNS suffix. Update our state and notify about the change.
	t.claimedMagicDNSSuffix = ipnState.MagicDNSSuffix

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
