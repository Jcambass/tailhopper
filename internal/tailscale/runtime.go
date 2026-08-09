package tailscale

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"

	"github.com/jcambass/tailhopper/internal/socks"
	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

type Info struct {
	Started bool
	Status  *ipnstate.Status
	Error   string
}

type tsnetBackend interface {
	Start() error
	Close() error
	Status(context.Context) (*ipnstate.Status, error)
	Dial(context.Context, string, string) (net.Conn, error)
}

type tsnetBackendFactory func() tsnetBackend

// Runtime owns one tsnet server and its local SOCKS proxy. The mutex is a
// resource-lifetime lock: readers hold it for the complete tsnet operation,
// and Start/Stop hold it while changing or closing resources.
type Runtime struct {
	port            int
	newTSNetBackend tsnetBackendFactory
	logger          *slog.Logger

	mu    sync.RWMutex
	tsnet tsnetBackend
	proxy *socks.Server
}

func NewRuntime(id int, stateDir, hostname string, port int) *Runtime {
	runtime := newRuntime(id, port, nil)
	runtime.newTSNetBackend = func() tsnetBackend {
		return newRealTSNetBackend(stateDir, hostname, runtime.tsnetLogf(slog.LevelDebug), runtime.tsnetLogf(slog.LevelInfo))
	}
	return runtime
}

func newRuntime(id, port int, factory tsnetBackendFactory) *Runtime {
	return &Runtime{
		port:            port,
		newTSNetBackend: factory,
		logger:          slog.Default().With(slog.String("component", "tailnet"), slog.Int("tailnet_id", id)),
	}
}

func (r *Runtime) Start(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.tsnet != nil {
		return nil
	}

	tsnetBackend := r.newTSNetBackend()
	if err := tsnetBackend.Start(); err != nil {
		_ = tsnetBackend.Close()
		return err
	}
	proxy, err := socks.NewServer(r.Dial, r.port)
	if err != nil {
		_ = tsnetBackend.Close()
		return err
	}

	r.tsnet = tsnetBackend
	r.proxy = proxy
	proxy.Start()
	return nil
}

func (r *Runtime) Stop(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.tsnet == nil {
		return nil
	}

	proxyErr := r.proxy.Close()
	backendErr := r.tsnet.Close()
	r.tsnet = nil
	r.proxy = nil
	if proxyErr != nil {
		return proxyErr
	}
	return backendErr
}

func (r *Runtime) Info(ctx context.Context) Info {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.tsnet == nil {
		return Info{}
	}
	status, err := r.tsnet.Status(ctx)
	if err != nil {
		return Info{Started: true, Error: err.Error()}
	}
	return Info{Started: true, Status: status}
}

func (r *Runtime) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.tsnet == nil {
		return nil, fmt.Errorf("tailnet is not running")
	}
	return r.tsnet.Dial(ctx, network, addr)
}

func (r *Runtime) tsnetLogf(level slog.Level) func(string, ...any) {
	return func(format string, args ...any) {
		msg := strings.TrimRight(fmt.Sprintf(format, args...), "\n")
		r.logger.Log(context.Background(), level, msg, slog.String("subcomponent", "tsnet"))
	}
}

type realTSNetBackend struct {
	server tsnet.Server
	client *local.Client
}

func newRealTSNetBackend(stateDir, hostname string, logf, userLogf func(string, ...any)) tsnetBackend {
	return &realTSNetBackend{server: tsnet.Server{
		Dir:      stateDir,
		Hostname: hostname,
		Logf:     logf,
		UserLogf: userLogf,
	}}
}

func (b *realTSNetBackend) Start() error {
	if err := b.server.Start(); err != nil {
		return err
	}
	client, err := b.server.LocalClient()
	if err != nil {
		return err
	}
	b.client = client
	return nil
}

func (b *realTSNetBackend) Close() error {
	return b.server.Close()
}

func (b *realTSNetBackend) Status(ctx context.Context) (*ipnstate.Status, error) {
	return b.client.Status(ctx)
}

func (b *realTSNetBackend) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return b.server.Dial(ctx, network, addr)
}
