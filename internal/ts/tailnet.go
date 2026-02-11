package ts

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/jcambass/tailhopper/internal/logging"
	"github.com/jcambass/tailhopper/internal/socks"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
	"tailscale.com/types/key"
)

type Tailnet struct {
	tsnetStateDir string
	Hostname      string

	State  *stateMachine
	logger *logging.Logger

	server     *tsnet.Server
	watcher    *watcher
	socksProxy *socks.Server

	needsStateRefreshRetry   *time.Timer
	needsStateRefreshRetryMu *sync.Mutex

	lifecycleMu *sync.RWMutex
}

func NewTailnet(tsnetStateDir string, hostname string, logger *logging.Logger) *Tailnet {
	if logger == nil {
		logger = logging.Default().With("component", "tailnet")
	}

	return &Tailnet{
		tsnetStateDir:            tsnetStateDir,
		Hostname:                 hostname,
		State:                    newStateMachine(),
		logger:                   logger,
		lifecycleMu:              &sync.RWMutex{},
		needsStateRefreshRetryMu: &sync.Mutex{},
	}
}

func (t *Tailnet) Start(ctx context.Context) error {
	if !t.lifecycleMu.TryLock() {
		return errors.New("tailnet is in the process of starting or stopping")
	}
	defer t.lifecycleMu.Unlock()

	if !t.State.Disabled() {
		return errors.New("tailnet that is not disabled cannot be started")
	}

	if t.State.Disabling() {
		return errors.New("tailnet that is currently disabling cannot be started")
	}

	t.logger.Printf("Starting tailnet")

	// Explicitly set the status to connecting BEFORE we do more work.
	t.State.SetConnecting(ctx, "tailnet_start")

	t.server = &tsnet.Server{
		Dir:      t.tsnetStateDir,
		Hostname: t.Hostname,
	}

	socksProxy, err := socks.NewServer(t.Dial)
	if err != nil {
		t.logger.Printf("failed to start SOCKS5 proxy: %v", err)
		t.State.SetDisabled(ctx, fmt.Sprintf("tailnet_start_failed: %v", err))
		return err
	}
	t.socksProxy = socksProxy
	t.socksProxy.Start()

	// start IPN watcher to observe state changes
	t.watcher = newWatcher(t)
	t.watcher.Start()

	// Asynchronously start the server
	err = t.server.Start()
	if err != nil {
		t.logger.Printf("failed to start tsnet server: %v", err)
		t.State.SetDisabled(ctx, fmt.Sprintf("tailnet_start_failed: %v", err))
		return err
	}

	return nil
}

func (t *Tailnet) Stop(ctx context.Context) error {
	if !t.lifecycleMu.TryLock() {
		return errors.New("tailnet is in the process of starting or stopping")
	}
	defer t.lifecycleMu.Unlock()

	if t.State.Disabling() {
		return errors.New("tailnet that is currently disabling cannot be stopped")
	}

	if t.State.Disabled() {
		return errors.New("tailnet that is disabled cannot be stopped")
	}

	t.logger.Printf("Stopping tailnet")

	// Mark as disabling so the UI can render a disconnecting state while shutdown is in flight.
	// We can't directly set to disabled here since we need to wait for the server and watcher to fully stop.
	// If we were to set to disabled immediately, we would allow connecting again before the server is fully stopped which would cause issues.
	// The lifecycleMu does the heavy lifting but the state is important for the UI.
	t.State.SetDisabling(ctx, "tailnet_stop")

	// Cancel any pending retry timers
	t.cancelRefreshRetry()

	// I'm a horrible person but I want to see our disabling state so we sleep for a moment here :sorry-not-sorry:
	time.Sleep(1 * time.Second)

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
			return err
		}
		t.server = nil
		t.logger.Printf("tsnet server stopped")
	}

	if t.socksProxy != nil {
		t.logger.Printf("Stopping SOCKS5 proxy")
		err := t.socksProxy.Close()
		if err != nil {
			t.logger.Printf("failed to close SOCKS5 proxy: %v", err)
			return err
		}
		t.socksProxy = nil
		t.logger.Printf("SOCKS5 proxy stopped")
	}

	// Set disabled after the server and watcher are fully stopped.
	t.State.SetDisabled(ctx, "tailnet_stopped")

	t.logger.Printf("Tailnet stopped successfully")

	return nil
}

func (t *Tailnet) RefreshState(ctx context.Context) (map[key.NodePublic]*ipnstate.PeerStatus, error) {
	if !t.lifecycleMu.TryRLock() {
		return nil, errors.New("tailnet is in the process of starting or stopping")
	}
	defer t.lifecycleMu.RUnlock()

	t.cancelRefreshRetry()

	t.logger.Printf("Refreshing tailnet state")

	if t.State.Disabling() {
		return nil, errors.New("tailnet that is currently disabling cannot refresh state")
	}

	if t.State.Disabled() {
		return nil, errors.New("tailnet that is disabled cannot refresh state")
	}

	lc, err := t.server.LocalClient()
	if err != nil {
		err = errors.New("failed to get local client: " + err.Error())
		t.logger.Println(err.Error())

		return nil, err
	}

	status, err := lc.Status(ctx)
	if err != nil {
		err = errors.New("failed to get status: " + err.Error())
		t.logger.Println(err.Error())

		return nil, err
	}

	// If the status is nil also fail
	if status == nil {
		err = errors.New("failed to get status: status is nil")
		t.logger.Println(err.Error())

		return nil, err
	}

	t.logger.Println("Tailnet state fetched successfully")

	if status.Self != nil {
		t.logger.Printf("Updating hostname from status: %s", status.Self.HostName)
		t.Hostname = status.Self.HostName
	}

	// TODO: Guard against connecting to a different tailnet than we had before?

	t.logger.Printf("Updating state machine based on backend state: %s", status.BackendState)
	switch status.BackendState {
	case ipn.NoState.String(), ipn.Stopped.String():
		// TODO: We can't set disabled here since that's different?
		// What should we do?
		// For now we just treat it as connecting
		t.State.SetConnecting(ctx, "backend_state_no_state_or_stopped")
	case ipn.Starting.String():
		t.State.SetConnecting(ctx, "backend_state_starting")
	case ipn.NeedsLogin.String():
		if status.AuthURL == "" {
			t.logger.Printf("AuthURL is not yet available in NeedsLogin state, keeping in current state and scheduling refresh retry")
			// Schedule retry if AuthURL is not yet available
			t.scheduleRefreshRetry(ctx, "NeedsLogin state missing AuthURL")
			t.logger.Printf("Exiting RefreshState early without updating state machine due to missing AuthURL in NeedsLogin state (async retry scheduled)")
			return status.Peer, nil
		} // AuthURL is available, transition to NeedsLogin state
		t.State.SetNeedsLogin(ctx, "backend_state_needs_login", status.AuthURL)

	case ipn.NeedsMachineAuth.String():
		if status.CurrentTailnet == nil || status.CurrentTailnet.MagicDNSSuffix == "" {
			t.logger.Printf("MagicDNSSuffix is not yet available in NeedsMachineAuth state, keeping in current state and scheduling refresh retry")
			// Schedule retry if MagicDNSSuffix is not yet available
			t.scheduleRefreshRetry(ctx, "NeedsMachineAuth state missing MagicDNSSuffix")
			t.logger.Printf("Exiting RefreshState early without updating state machine due to missing MagicDNSSuffix in NeedsMachineAuth state (async retry scheduled)")
			return status.Peer, nil
		}
		t.State.SetNeedsMachineAuth(ctx, "backend_state_needs_machine_auth", status.CurrentTailnet.MagicDNSSuffix)
	case ipn.Running.String():
		if status.CurrentTailnet == nil || status.CurrentTailnet.MagicDNSSuffix == "" {
			t.logger.Printf("MagicDNSSuffix is not yet available in Running state, keeping in current state and scheduling refresh retry")
			// Schedule retry if MagicDNSSuffix is not yet available
			t.scheduleRefreshRetry(ctx, "Running state missing MagicDNSSuffix")
			t.logger.Printf("Exiting RefreshState early without updating state machine due to missing MagicDNSSuffix in Running state (async retry scheduled)")
			return status.Peer, nil
		}
		t.State.SetConnected(ctx, "backend_state_running", status.CurrentTailnet.MagicDNSSuffix)
	default:
		panic("unknown backend state: " + status.BackendState)
	}

	t.logger.Printf("Tailnet state refreshed successfully: %s", t.State.Description())

	return status.Peer, nil
}

func (t *Tailnet) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	if !t.lifecycleMu.TryRLock() {
		return nil, errors.New("tailnet is in the process of starting or stopping")
	}
	defer t.lifecycleMu.RUnlock()

	if t.State.Disabling() {
		return nil, errors.New("tailnet that is currently disabling cannot dial")
	}

	if t.State.Disabled() {
		return nil, errors.New("tailnet that is disabled cannot dial")
	}

	return t.server.Dial(ctx, network, address)
}

func (t *Tailnet) SocksAddr() string {
	if t.socksProxy == nil {
		panic("socks proxy is not initialized")
	}

	return t.socksProxy.Addr()
}

// cancelRefreshRetry cancels any pending state refresh retry.
func (t *Tailnet) cancelRefreshRetry() {
	t.needsStateRefreshRetryMu.Lock()
	if t.needsStateRefreshRetry != nil {
		t.needsStateRefreshRetry.Stop()
		t.needsStateRefreshRetry = nil
	}
	t.needsStateRefreshRetryMu.Unlock()
}

// scheduleRefreshRetry schedules a state refresh to be retried.
func (t *Tailnet) scheduleRefreshRetry(ctx context.Context, reason string) {
	t.needsStateRefreshRetryMu.Lock()
	defer t.needsStateRefreshRetryMu.Unlock()

	t.logger.Printf("Scheduling state refresh retry: %s", reason)
	if t.needsStateRefreshRetry != nil {
		t.needsStateRefreshRetry.Stop()
	}
	t.needsStateRefreshRetry = time.AfterFunc(500*time.Millisecond, func() {
		t.logger.Printf("Retrying state refresh: %s", reason)
		_, err := t.RefreshState(ctx)
		if err != nil {
			t.logger.Printf("State refresh retry failed: %v", err)
		}
	})
}
