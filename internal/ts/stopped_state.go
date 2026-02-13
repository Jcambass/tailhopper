package ts

import (
	"context"
	"errors"
	"net"

	"tailscale.com/tailcfg"
)

type StoppedState struct {
	tailnet *Tailnet
}

func (s *StoppedState) Name() StateName {
	return StoppedStateName
}

func (s *StoppedState) Start(ctx context.Context) error {
	return s.tailnet.start(ctx)
}

func (s *StoppedState) Stop(ctx context.Context) error {
	return errors.New("unable to stop: tailnet is already stopped")
}

func (s *StoppedState) Logout(ctx context.Context) error {
	return s.tailnet.logout(ctx)
}

func (s *StoppedState) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return nil, errors.New("unable to dial: tailnet is stopped")
}

func (s *StoppedState) LoginURL() (string, error) {
	return "", errors.New("unable to get login URL: tailnet is stopped")
}

func (s *StoppedState) Peers() ([]tailcfg.NodeView, error) {
	return nil, errors.New("unable to get peers: tailnet is stopped")
}

func (s *StoppedState) TerminalError() (string, error) {
	return "", errors.New("unable to get terminal error: tailnet is stopped")
}

func (s *StoppedState) ReactToIPNStateChange(ctx context.Context, ipnState IPNState) error {
	// Simply ignore IPN state changes while in the stopped state.
	return nil
}
