package ts

import (
	"context"
	"errors"
	"net"

	"tailscale.com/tailcfg"
)

type NeedsLoginState struct {
	tailnet *Tailnet
}

func (s *NeedsLoginState) Name() StateName {
	return NeedsLoginStateName
}

func (s *NeedsLoginState) Start(ctx context.Context) error {
	return errors.New("unable to start: tailnet is already started (needs login)")
}

func (s *NeedsLoginState) Stop(ctx context.Context) error {
	return s.tailnet.stop(ctx)
}

func (s *NeedsLoginState) Logout(ctx context.Context) error {
	return errors.New("unable to logout: tailnet needs login")
}

func (s *NeedsLoginState) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return nil, errors.New("unable to dial: tailnet needs login")
}

func (s *NeedsLoginState) LoginURL() (string, error) {
	return s.tailnet.loginURL, nil
}

func (s *NeedsLoginState) Peers() ([]tailcfg.NodeView, error) {
	return nil, errors.New("unable to get peers: tailnet needs login")
}

func (s *NeedsLoginState) TerminalError() (string, error) {
	return "", errors.New("unable to get terminal error: tailnet needs login")
}

func (s *NeedsLoginState) ReactToIPNStateChange(ctx context.Context, ipnState IPNState) error {
	s.tailnet.maybeTransitionToNeedsMachineAuth(ipnState)
	s.tailnet.maybeTransitionToConnected(ipnState)
	return nil
}
