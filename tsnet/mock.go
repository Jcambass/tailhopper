package tsnet

import (
	"context"
	"net"

	"tailscale.com/ipn"
	"tailscale.com/types/logger"
)

var _ TSNetServer = (*MockTSNetServer)(nil)

// MockTSNetServer is a mock implementation of TSNetServer for testing.
type MockTSNetServer struct {
	Hostname  string
	Dir       string
	AuthKey   string
	Ephemeral bool
	Logf      logger.Logf

	StartFunc       func() error
	CloseFunc       func() error
	LocalClientFunc func() (LocalClient, error)
	DialFunc        func(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewMockTSNetServer creates a new MockTSNetServer instance.
func NewMockTSNetServer() *MockTSNetServer {
	return &MockTSNetServer{
		StartFunc: func() error {
			return nil
		},
		CloseFunc: func() error {
			return nil
		},
		LocalClientFunc: func() (LocalClient, error) {
			return &MockLocalClient{}, nil
		},
		DialFunc: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, nil
		},
	}
}

// Start implements TSNetServer.
func (m *MockTSNetServer) Start() error {
	return m.StartFunc()
}

// Close implements TSNetServer.
func (m *MockTSNetServer) Close() error {
	return m.CloseFunc()
}

// LocalClient implements TSNetServer.
func (m *MockTSNetServer) LocalClient() (LocalClient, error) {
	return m.LocalClientFunc()
}

func (m *MockTSNetServer) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return m.DialFunc(ctx, network, addr)
}

// MockLocalClient is a mock implementation of LocalClient for testing.
type MockLocalClient struct {
	LogoutFunc      func(ctx context.Context) error
	WatchIPNBusFunc func(ctx context.Context, mask ipn.NotifyWatchOpt) (IPNBusWatcher, error)
}

func (c *MockLocalClient) Logout(ctx context.Context) error {
	return c.LogoutFunc(ctx)
}

func (c *MockLocalClient) WatchIPNBus(ctx context.Context, mask ipn.NotifyWatchOpt) (IPNBusWatcher, error) {
	if c.WatchIPNBusFunc != nil {
		return c.WatchIPNBusFunc(ctx, mask)
	}
	return &MockIPNBusWatcher{}, nil
}

// MockIPNBusWatcher is a mock implementation of IPNBusWatcher for testing.
type MockIPNBusWatcher struct {
	NextFunc  func() (ipn.Notify, error)
	CloseFunc func() error
}

func (w *MockIPNBusWatcher) Next() (ipn.Notify, error) {
	if w.NextFunc != nil {
		return w.NextFunc()
	}
	return ipn.Notify{}, nil
}

func (w *MockIPNBusWatcher) Close() error {
	if w.CloseFunc != nil {
		return w.CloseFunc()
	}
	return nil
}
