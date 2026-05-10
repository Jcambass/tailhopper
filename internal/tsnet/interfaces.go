// Package tsnet provides interfaces and implementations for Tailscale tsnet integration.
package tsnet

import (
	"context"
	"net"

	"tailscale.com/ipn"
)

// TSNetServer is an interface that abstracts tsnet.Server functionality
// to enable testing without real network connections.
type TSNetServer interface {
	// Start initializes the server connection to Tailscale.
	Start() error

	// Close shuts down the server.
	Close() error

	// LocalClient returns a LocalClient for this server.
	LocalClient() (LocalClient, error)

	// Dial connects to the address on the tailnet.
	Dial(ctx context.Context, network, addr string) (net.Conn, error)
}

// LocalClient is an interface for tailscale LocalClient operations.
type LocalClient interface {
	// Logout logs out the current node.
	Logout(ctx context.Context) error

	// WatchIPNBus subscribes to the IPN notification bus. It returns a watcher
	// once the bus is connected successfully.
	WatchIPNBus(ctx context.Context, mask ipn.NotifyWatchOpt) (IPNBusWatcher, error)
}

// IPNBusWatcher is an active subscription (watch) of the local tailscaled IPN bus.
type IPNBusWatcher interface {
	// Next returns the next ipn.Notify from the stream.
	Next() (ipn.Notify, error)
	// Close stops the watcher and releases its resources.
	Close() error
}

// TSNetServerConfig holds configuration for creating a new TSNetServer.
type TSNetServerConfig struct {
	Dir      string
	Hostname string
	Logf     func(string, ...any)
	UserLogf func(string, ...any)
}

// TSNetServerFactory is a function that creates new TSNetServer instances.
type TSNetServerFactory func(config TSNetServerConfig) TSNetServer
