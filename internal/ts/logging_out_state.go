package ts

import (
	"context"
	"errors"
	"net"

	"tailscale.com/tailcfg"
)

type LoggingOutState struct {
	tailnet *Tailnet
}

func (s *LoggingOutState) Name() StateName {
	return LoggingOutStateName
}

func (s *LoggingOutState) Start(ctx context.Context) error {
	return errors.New("unable to start: tailnet is logging out")
}

func (s *LoggingOutState) Stop(ctx context.Context) error {
	return errors.New("unable to stop: tailnet is logging out")
}

func (s *LoggingOutState) Logout(ctx context.Context) error {
	return errors.New("unable to logout: tailnet is already logging out")
}

func (s *LoggingOutState) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return nil, errors.New("unable to dial: tailnet is logging out")
}

func (s *LoggingOutState) LoginURL() (string, error) {
	return "", errors.New("unable to get login URL: tailnet is logging out")
}

func (s *LoggingOutState) Peers() ([]tailcfg.NodeView, error) {
	return nil, errors.New("unable to get peers: tailnet is logging out")
}

func (s *LoggingOutState) TerminalError() (string, error) {
	return "", errors.New("unable to get terminal error: tailnet is logging out")
}

func (s *LoggingOutState) ReactToIPNStateChange(ctx context.Context, ipnState IPNState) error {
	// Simply ignore IPN state changes while in the logging out state.
	return nil
}
