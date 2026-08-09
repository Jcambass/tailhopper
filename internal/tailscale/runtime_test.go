package tailscale

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"
)

type fakeBackend struct {
	start  func() error
	close  func() error
	status func(context.Context) (*ipnstate.Status, error)
	dial   func(context.Context, string, string) (net.Conn, error)
}

func (b *fakeBackend) Start() error { return b.start() }
func (b *fakeBackend) Close() error { return b.close() }
func (b *fakeBackend) Status(ctx context.Context) (*ipnstate.Status, error) {
	return b.status(ctx)
}
func (b *fakeBackend) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return b.dial(ctx, network, addr)
}

func runtimeFixture(t *testing.T, status func(context.Context) (*ipnstate.Status, error)) (*Runtime, *fakeBackend) {
	t.Helper()
	fake := &fakeBackend{
		start:  func() error { return nil },
		close:  func() error { return nil },
		status: status,
		dial:   func(context.Context, string, string) (net.Conn, error) { return nil, nil },
	}
	runtime := newRuntime(1, 0, func() tsnetBackend {
		return fake
	})
	return runtime, fake
}

func TestRuntimeStartInfoStop(t *testing.T) {
	status := &ipnstate.Status{BackendState: "Running"}
	runtime, tsnetBackend := runtimeFixture(t, func(context.Context) (*ipnstate.Status, error) { return status, nil })
	closed := false
	tsnetBackend.close = func() error {
		closed = true
		return nil
	}

	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	info := runtime.Info(context.Background())
	if !info.Started || info.Status != status || info.Error != "" {
		t.Fatalf("unexpected info: %#v", info)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if !closed || runtime.Info(context.Background()).Started {
		t.Fatal("runtime resources remained active")
	}
}

func TestRuntimeStartFailureClosesServer(t *testing.T) {
	runtime, tsnetBackend := runtimeFixture(t, func(context.Context) (*ipnstate.Status, error) { return &ipnstate.Status{}, nil })
	tsnetBackend.start = func() error { return fmt.Errorf("start failed") }
	closed := false
	tsnetBackend.close = func() error {
		closed = true
		return nil
	}

	if err := runtime.Start(context.Background()); err == nil {
		t.Fatal("expected Start error")
	}
	if !closed {
		t.Error("failed server was not closed")
	}
}

func TestRuntimeStopWaitsForStatus(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	runtime, _ := runtimeFixture(t, func(context.Context) (*ipnstate.Status, error) {
		close(entered)
		<-release
		return &ipnstate.Status{}, nil
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	infoDone := make(chan struct{})
	go func() {
		defer close(infoDone)
		runtime.Info(context.Background())
	}()
	<-entered
	stopDone := make(chan error, 1)
	go func() { stopDone <- runtime.Stop(context.Background()) }()
	assertBlocked(t, stopDone)
	close(release)
	<-infoDone
	if err := <-stopDone; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeStopWaitsForDial(t *testing.T) {
	runtime, tsnetBackend := runtimeFixture(t, func(context.Context) (*ipnstate.Status, error) { return &ipnstate.Status{}, nil })
	entered := make(chan struct{})
	release := make(chan struct{})
	tsnetBackend.dial = func(context.Context, string, string) (net.Conn, error) {
		close(entered)
		<-release
		return nil, nil
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	dialDone := make(chan struct{})
	go func() {
		defer close(dialDone)
		_, _ = runtime.Dial(context.Background(), "tcp", "example.com:80")
	}()
	<-entered
	stopDone := make(chan error, 1)
	go func() { stopDone <- runtime.Stop(context.Background()) }()
	assertBlocked(t, stopDone)
	close(release)
	<-dialDone
	if err := <-stopDone; err != nil {
		t.Fatal(err)
	}
}

func assertBlocked(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("Stop completed while resource was in use: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
}
