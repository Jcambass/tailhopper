package ts

import (
	"context"
	"errors"
	"net"

	"tailscale.com/tailcfg"
)

type StoppingState struct {
	tailnet *Tailnet
}

func (s *StoppingState) Name() StateName {
	return StoppingStateName
}

func (s *StoppingState) Start(ctx context.Context) error {
	return errors.New("unable to start: tailnet is stopping")
}

func (s *StoppingState) Stop(ctx context.Context) error {
	return errors.New("unable to stop: tailnet is already stopping")
}

func (s *StoppingState) Logout(ctx context.Context) error {
	return errors.New("unable to logout: tailnet is stopping")
}

func (s *StoppingState) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return nil, errors.New("unable to dial: tailnet is stopping")
}

func (s *StoppingState) LoginURL() (string, error) {
	return "", errors.New("unable to get login URL: tailnet is stopping")
}

func (s *StoppingState) Peers() ([]tailcfg.NodeView, error) {
	return nil, errors.New("unable to get peers: tailnet is stopping")
}

func (s *StoppingState) TerminalError() (string, error) {
	return "", errors.New("unable to get terminal error: tailnet is stopping")
}

func (s *StoppingState) ReactToIPNStateChange(ctx context.Context, ipnState IPNState) error {
	// Simply ignore IPN state changes while in the stopping state.
	return nil
}
