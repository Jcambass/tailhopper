package ts

import (
	"context"
	"errors"
	"log"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
	"net"
)

type Tailnet struct {
	tsnetStateDir string
	Hostname      string

	State *stateMachine

	server  *tsnet.Server
	watcher *watcher
}

func NewTailnet(tsnetStateDir string, hostname string) *Tailnet {
	return &Tailnet{
		tsnetStateDir: tsnetStateDir,
		Hostname:      hostname,
		State:         newStateMachine(),
	}
}

func (t *Tailnet) Start() error {
	if !t.State.Disabled() {
		return errors.New("tailnet that is not disabled cannot be started")
	}

	log.Printf("Starting tailnet")

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
		log.Printf("failed to start tsnet server: %v", err)
		err := t.State.SetFailed(err)
		if err != nil {
			log.Printf("failed to set state to failed: %v", err)
		}
		return err
	}

	return nil
}

func (t *Tailnet) Stop() error {
	if t.State.Disabled() {
		return errors.New("tailnet that is disabled cannot be stopped")
	}

	log.Printf("Stopping tailnet")

	if t.watcher != nil {
		t.watcher.Stop()
	}
	if t.server != nil {
		err := t.server.Close()
		if err != nil {
			log.Printf("failed to close tsnet server: %v", err)
			return err
		}
	}

	// Explicitly set state to disabled on stop.
	err := t.State.SetDisabled()
	if err != nil {
		log.Printf("failed to set state to disabled: %v", err)
		return err
	}

	return nil
}

func (t *Tailnet) Status(ctx context.Context) (*ipnstate.Status, error) {
	if t.State.Disabled() {
		return nil, errors.New("tailnet that is disabled cannot get status")
	}

	log.Println("Getting tailnet status")

	lc, err := t.server.LocalClient()
	if err != nil {
		log.Printf("failed to get local client: %v", err)
		log.Printf("Setting state to failed due to local client error")
		err := t.State.SetFailed(err)
		if err != nil {
			log.Printf("failed to set state to failed: %v", err)
		}
		return nil, err
	}

	status, err := lc.Status(ctx)
	if err != nil {
		log.Printf("failed to get status: %v", err)
		log.Printf("Setting state to failed due to status error")
		err := t.State.SetConnecting() // Set to connecting on error
		if err != nil {
			log.Printf("failed to set state to connecting: %v", err)
		}
		return nil, err
	}

	// Update our hostname from status.Self if available
	if status.Self != nil {
		log.Printf("Updating hostname from status: %s", status.Self.HostName)
		t.Hostname = status.Self.HostName
	}

	log.Printf("Tailnet status retrieved successfully: %v", status)

	return status, nil
}

func (t *Tailnet) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	if t.State.Disabled() {
		return nil, errors.New("tailnet that is disabled cannot dial")
	}

	return t.server.Dial(ctx, network, address)
}