package ts

import (
	"context"
	"errors"
	"net"

	"tailscale.com/tailcfg"
)

type StartingState struct {
	tailnet *Tailnet
}

func (s *StartingState) Name() StateName {
	return StartingStateName
}

func (s *StartingState) Start(ctx context.Context) error {
	return errors.New("unable to start: tailnet is already starting")
}

func (s *StartingState) Stop(ctx context.Context) error {
	return errors.New("unable to stop: tailnet is already starting")
}

func (s *StartingState) Logout(ctx context.Context) error {
	return errors.New("unable to logout: tailnet is starting")
}

func (s *StartingState) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return nil, errors.New("unable to dial: tailnet is starting")
}

func (s *StartingState) LoginURL() (string, error) {
	return "", errors.New("unable to get login URL: tailnet is starting")
}

func (s *StartingState) Peers() ([]tailcfg.NodeView, error) {
	return nil, errors.New("unable to get peers: tailnet is starting")
}

func (s *StartingState) TerminalError() (string, error) {
	return "", errors.New("unable to get terminal error: tailnet is starting")
}

func (s *StartingState) ReactToIPNStateChange(ctx context.Context, ipnState IPNState) error {
	// We don't want to react to IPN state changes while in the starting state, as we are still waiting for the initial state from IPN.
	// TODO: Check if that makes sense.
	return nil
}
