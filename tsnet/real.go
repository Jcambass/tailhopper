package tsnet

import (
	"context"
	"log/slog"
	"net"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

var _ TSNetServer = (*RealTSNetServer)(nil)

// RealTSNetServer wraps a real tsnet.Server to implement TSNetServer.
type RealTSNetServer struct {
	tsnet.Server
}

// NewRealTSNetServer creates a new RealTSNetServer instance.
func NewRealTSNetServer(serviceName string) *RealTSNetServer {
	server := &RealTSNetServer{}
	adapter := tsnetLogAdapter(serviceName)
	server.Logf = adapter     // Backend/debugging logs
	server.UserLogf = adapter // User-facing logs (AuthURL, etc.)
	return server
}

// Start implements TSNetServer.
func (s *RealTSNetServer) Start() error {
	start := time.Now()
	slog.Debug("tsnet server Start() called",
		"hostname", s.Hostname,
		"ephemeral", s.Ephemeral,
		"dir", s.Dir,
		"has_auth_key", s.AuthKey != "",
	)

	err := s.Server.Start()

	if err != nil {
		slog.Debug("tsnet server Start() failed",
			"hostname", s.Hostname,
			"duration", time.Since(start),
			"error", err,
		)
	} else {
		slog.Debug("tsnet server Start() succeeded",
			"hostname", s.Hostname,
			"duration", time.Since(start),
		)
	}

	return err
}

// Close implements TSNetServer.
func (s *RealTSNetServer) Close() error {
	return s.Server.Close()
}

// LocalClient implements TSNetServer.
func (s *RealTSNetServer) LocalClient() (LocalClient, error) {
	lc, err := s.Server.LocalClient()
	if err != nil {
		return nil, err
	}
	return &RealLocalClient{lc: lc}, nil
}

// Dial implements TSNetServer.
func (s *RealTSNetServer) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return s.Server.Dial(ctx, network, addr)
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
