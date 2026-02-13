package ts

import (
	"context"
	"errors"
	"net"

	"tailscale.com/tailcfg"
)

type NeedsMachineAuthState struct {
	tailnet *Tailnet
}

func (s *NeedsMachineAuthState) Name() StateName {
	return NeedsMachineAuthStateName
}

func (s *NeedsMachineAuthState) Start(ctx context.Context) error {
	return errors.New("unable to start: tailnet is already started (needs machine auth)")
}

func (s *NeedsMachineAuthState) Stop(ctx context.Context) error {
	return s.tailnet.stop(ctx)
}

func (s *NeedsMachineAuthState) Logout(ctx context.Context) error {
	return s.tailnet.logout(ctx)
}

func (s *NeedsMachineAuthState) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return nil, errors.New("unable to dial: tailnet needs machine auth")
}

func (s *NeedsMachineAuthState) LoginURL() (string, error) {
	return "", errors.New("unable to get login URL: tailnet needs machine auth")
}

func (s *NeedsMachineAuthState) Peers() ([]tailcfg.NodeView, error) {
	return nil, errors.New("unable to get peers: tailnet needs machine auth")
}

func (s *NeedsMachineAuthState) TerminalError() (string, error) {
	return "", errors.New("unable to get terminal error: tailnet needs machine auth")
}

func (s *NeedsMachineAuthState) ReactToIPNStateChange(ctx context.Context, ipnState IPNState) error {
	s.tailnet.maybeClaimMagicDNSSuffix(ipnState)
	s.tailnet.maybeTransitionToNeedsLogin(ipnState)
	s.tailnet.maybeTransitionToConnected(ipnState)
	return nil
}
