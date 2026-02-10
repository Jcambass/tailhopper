package ts

import (
	"context"
	"errors"
	"net"

	"github.com/jcambass/tailhopper/internal/logging"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

type Tailnet struct {
	tsnetStateDir string
	Hostname      string

	State  *stateMachine
	logger *logging.Logger

	server  *tsnet.Server
	watcher *watcher
}

func NewTailnet(tsnetStateDir string, hostname string, logger *logging.Logger) *Tailnet {
	if logger == nil {
		logger = logging.Default().With("component", "tailnet")
	}
	return &Tailnet{
		tsnetStateDir: tsnetStateDir,
		Hostname:      hostname,
		State:         newStateMachine(),
		logger:        logger,
	}
}

func (t *Tailnet) Start(ctx context.Context) error {
	if !t.State.Disabled() {
		return errors.New("tailnet that is not disabled cannot be started")
	}

	logger := logging.FromContext(ctx).With("component", "tailnet")
	logger.Printf("Starting tailnet")

	t.server = &tsnet.Server{
		Dir:      t.tsnetStateDir,
		Hostname: t.Hostname,
	}

	// start IPN watcher to observe state changes
	t.watcher = newWatcher(t)
	t.watcher.Start()

	// Asynchronously start the server
	err := t.server.Start()
	if err != nil {
		logger.Printf("failed to start tsnet server: %v", err)
		t.State.SetFailed(ctx, "tailnet_start_failed", err)
		return err
	}

	// Explicitly set the status to connecting
	t.State.SetConnecting(ctx, "tailnet_start")

	return nil
}

func (t *Tailnet) Stop(ctx context.Context) error {
	if t.State.Disabled() {
		return errors.New("tailnet that is disabled cannot be stopped")
	}

	logger := logging.FromContext(ctx).With("component", "tailnet")
	logger.Printf("Stopping tailnet")

	// Explicitly set state to disabled on stop before stopping the server and watcher to ensure
	// that any in-flight operations are aware of the disabled state and don't try to interact with the server or dial new connections.
	t.State.SetDisabled(ctx)

	if t.watcher != nil {
		logger.Printf("Stopping IPN watcher")
		t.watcher.Stop()
		logger.Printf("IPN watcher stopped")
	}
	if t.server != nil {
		logger.Printf("Stopping tsnet server")
		err := t.server.Close()
		if err != nil {
			logger.Printf("failed to close tsnet server: %v", err)
			return err
		}
		logger.Printf("tsnet server stopped")
	}

	logger.Printf("Tailnet stopped successfully")

	return nil
}

func (t *Tailnet) RefreshState(ctx context.Context) (*ipnstate.Status, error) {
	logger := logging.FromContext(ctx).With("component", "tailnet")
	logger.Printf("Refreshing tailnet state")

	if t.State.Disabled() {
		return nil, errors.New("tailnet that is disabled cannot refresh state")
	}

	lc, err := t.server.LocalClient()
	if err != nil {
		err = errors.New("failed to get local client: " + err.Error())
		logger.Println(err.Error())
		t.State.SetFailed(ctx, "refresh_state", err)

		return nil, err
	}

	status, err := lc.Status(ctx)
	if err != nil {
		err = errors.New("failed to get status: " + err.Error())
		logger.Println(err.Error())
		t.State.SetFailed(ctx, "refresh_state", err)

		return nil, err
	}

	// If the status is nil also fail
	if status == nil {
		err = errors.New("failed to get status: status is nil")
		logger.Println(err.Error())
		t.State.SetFailed(ctx, "refresh_state", err)

		return nil, err
	}

	logger.Println("Tailnet state fetched successfully")

	if status.Self != nil {
		logger.Printf("Updating hostname from status: %s", status.Self.HostName)
		t.Hostname = status.Self.HostName
	}

	// TODO: Guard against connecting to a different tailnet than we had before?

	logger.Printf("Updating state machine based on backend state: %s", status.BackendState)
	switch status.BackendState {
	case ipn.NoState.String(), ipn.Stopped.String():
		// TODO: We can't set disabled here since that's different?
		// What should we do?
		// For now we just treat it as connecting
		t.State.SetConnecting(ctx, "backend_state_no_state_or_stopped")
	case ipn.Starting.String():
		t.State.SetConnecting(ctx, "backend_state_starting")
	case ipn.NeedsLogin.String():
		t.State.SetNeedsLogin(ctx, "backend_state_needs_login", status.AuthURL)
	case ipn.NeedsMachineAuth.String():
		t.State.SetNeedsMachineAuth(ctx, "backend_state_needs_machine_auth", status.CurrentTailnet.MagicDNSSuffix)
	case ipn.Running.String():
		t.State.SetConnected(ctx, "backend_state_running", status.CurrentTailnet.MagicDNSSuffix)
	default:
		panic("unknown backend state: " + status.BackendState)
	}

	logger.Printf("Tailnet state refreshed successfully: %s", t.State.Description())

	return status, nil
}

func (t *Tailnet) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	if t.State.Disabled() {
		return nil, errors.New("tailnet that is disabled cannot dial")
	}

	return t.server.Dial(ctx, network, address)
}
