package ts

import (
	"context"
	"errors"
	"net"

	"tailscale.com/tailcfg"
)

type StartedState struct {
	tailnet *Tailnet
}

func (s *StartedState) Name() StateName {
	return StartedStateName
}

func (s *StartedState) Start(ctx context.Context) error {
	return errors.New("unable to start: tailnet is already started")
}

func (s *StartedState) Stop(ctx context.Context) error {
	return s.tailnet.stop(ctx)
}

// We might not be properly logged in yet but the tsnet server is up and calls to its Logout method will succeed.
func (s *StartedState) Logout(ctx context.Context) error {
	return s.tailnet.logout(ctx)
}

func (s *StartedState) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return nil, errors.New("unable to dial: tailnet is started but not connected yet")
}

func (s *StartedState) LoginURL() (string, error) {
	return "", errors.New("unable to get login URL: tailnet is started but not connected yet")
}

func (s *StartedState) Peers() ([]tailcfg.NodeView, error) {
	return nil, errors.New("unable to get peers: tailnet is started but not connected yet")
}

func (s *StartedState) TerminalError() (string, error) {
	return "", errors.New("unable to get terminal error: tailnet is started but not connected yet")
}

func (s *StartedState) ReactToIPNStateChange(ctx context.Context, ipnState IPNState) error {
	s.tailnet.maybeTransitionToNeedsLogin(ipnState)
	s.tailnet.maybeTransitionToNeedsMachineAuth(ipnState)
	s.tailnet.maybeTransitionToConnected(ipnState)
	return nil
}
