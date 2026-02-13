package ts

import (
	"context"
	"errors"
	"net"

	"tailscale.com/tailcfg"
)

type HasTerminalErrorState struct {
	tailnet *Tailnet
}

func (s *HasTerminalErrorState) Name() StateName {
	return HasTerminalErrorStateName
}

func (s *HasTerminalErrorState) Start(ctx context.Context) error {
	return errors.New("unable to start: tailnet is already started (has terminal error)")
}

// TODO: Allow or not?
func (s *HasTerminalErrorState) Stop(ctx context.Context) error {
	return s.tailnet.stop(ctx)
}

// TODO: Allow or not?
func (s *HasTerminalErrorState) Logout(ctx context.Context) error {
	return s.tailnet.logout(ctx)
}

func (s *HasTerminalErrorState) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return nil, errors.New("unable to dial: tailnet has terminal error")
}

func (s *HasTerminalErrorState) LoginURL() (string, error) {
	return "", errors.New("unable to get login URL: tailnet has terminal error")
}

func (s *HasTerminalErrorState) Peers() ([]tailcfg.NodeView, error) {
	return nil, errors.New("unable to get peers: tailnet has terminal error")
}

func (s *HasTerminalErrorState) TerminalError() (string, error) {
	return s.tailnet.terminalError, nil
}

func (s *HasTerminalErrorState) ReactToIPNStateChange(ctx context.Context, ipnState IPNState) error {
	// Simply ignore IPN state changes while in the terminal error state.
	return nil
}
