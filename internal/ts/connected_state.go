package ts

import (
	"context"
	"errors"
	"net"

	"tailscale.com/tailcfg"
)

type ConnectedState struct {
	tailnet *Tailnet
}

func (s *ConnectedState) Name() StateName {
	return ConnectedStateName
}

func (s *ConnectedState) Start(ctx context.Context) error {
	return errors.New("unable to start: tailnet is already connected")
}

func (s *ConnectedState) Stop(ctx context.Context) error {
	return s.tailnet.stop(ctx)
}

func (s *ConnectedState) Logout(ctx context.Context) error {
	return s.tailnet.logout(ctx)
}

func (s *ConnectedState) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return s.tailnet.server.Dial(ctx, network, addr)
}

func (s *ConnectedState) LoginURL() (string, error) {
	return "", errors.New("unable to get login URL: tailnet is connected")
}

func (s *ConnectedState) Peers() ([]tailcfg.NodeView, error) {
	return s.tailnet.peers, nil
}

func (s *ConnectedState) TerminalError() (string, error) {
	return "", errors.New("unable to get terminal error: tailnet is connected")
}

func (s *ConnectedState) ReactToIPNStateChange(ctx context.Context, ipnState IPNState) error {
	s.tailnet.maybeClaimMagicDNSSuffix(ipnState)
	s.tailnet.maybeTransitionToNeedsLogin(ipnState)
	s.tailnet.maybeTransitionToNeedsMachineAuth(ipnState)
	s.tailnet.maybeUpdatePeers(ipnState)
	return nil
}
