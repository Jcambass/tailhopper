package tsnet

import (
	"context"
	"net"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

var _ TSNetServer = (*RealTSNetServer)(nil)

// RealTSNetServer wraps a real tsnet.Server to implement TSNetServer.
type RealTSNetServer struct {
	server tsnet.Server
}

// NewRealTSNetServer creates a new RealTSNetServer with the given config.
func NewRealTSNetServer(config TSNetServerConfig) TSNetServer {
	return &RealTSNetServer{
		server: tsnet.Server{
			Dir:      config.Dir,
			Hostname: config.Hostname,
			Logf:     config.Logf,
			UserLogf: config.UserLogf,
		},
	}
}

// Start implements TSNetServer.
func (s *RealTSNetServer) Start() error {
	return s.server.Start()
}

// Close implements TSNetServer.
func (s *RealTSNetServer) Close() error {
	return s.server.Close()
}

// LocalClient implements TSNetServer.
func (s *RealTSNetServer) LocalClient() (LocalClient, error) {
	lc, err := s.server.LocalClient()
	if err != nil {
		return nil, err
	}
	return &RealLocalClient{lc: lc}, nil
}

// Dial implements TSNetServer.
func (s *RealTSNetServer) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return s.server.Dial(ctx, network, addr)
}

// RealLocalClient wraps a real local.Client.
type RealLocalClient struct {
	lc *local.Client
}

// Logout logs out the current node.
func (c *RealLocalClient) Logout(ctx context.Context) error {
	return c.lc.Logout(ctx)
}

// WatchIPNBus subscribes to the IPN notification bus. It returns a watcher
// once the bus is connected successfully.
func (c *RealLocalClient) WatchIPNBus(ctx context.Context, mask ipn.NotifyWatchOpt) (IPNBusWatcher, error) {
	watcher, err := c.lc.WatchIPNBus(ctx, mask)
	if err != nil {
		return nil, err
	}
	return &RealIPNBusWatcher{watcher: watcher}, nil
}

// RealIPNBusWatcher wraps a real IPNBusWatcher.
type RealIPNBusWatcher struct {
	watcher *local.IPNBusWatcher
}

// Next implements IPNBusWatcher.
func (w *RealIPNBusWatcher) Next() (ipn.Notify, error) {
	return w.watcher.Next()
}

// Close implements IPNBusWatcher.
func (w *RealIPNBusWatcher) Close() error {
	return w.watcher.Close()
}
