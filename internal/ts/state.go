package ts

import (
	"context"
	"net"

	"tailscale.com/tailcfg"
)

type StateName string

const (
	ConnectedStateName        StateName = "ConnectedState"
	HasTerminalErrorStateName StateName = "HasTerminalErrorState"
	NeedsLoginStateName       StateName = "NeedsLoginState"
	NeedsMachineAuthStateName StateName = "NeedsMachineAuthState"
	StartedStateName          StateName = "StartedState"
	StartingStateName         StateName = "StartingState"
	StoppedStateName          StateName = "StoppedState"
	StoppingStateName         StateName = "StoppingState"
	LoggingOutStateName       StateName = "LoggingOutState"
)

type State interface {
	Name() StateName

	// Operations
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Logout(ctx context.Context) error
	Dial(ctx context.Context, network, addr string) (net.Conn, error)

	// Data Getters
	LoginURL() (string, error)
	Peers() ([]tailcfg.NodeView, error)
	TerminalError() (string, error)

	// Data Setters
	ReactToIPNStateChange(ctx context.Context, ipnState IPNState) error
}
