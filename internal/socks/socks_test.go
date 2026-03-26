package socks

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"

	"golang.org/x/net/proxy"
)

func TestNewServer(t *testing.T) {
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, nil
	}

	srv, err := NewServer(dial, 0) // port 0 = OS picks free port
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()

	if srv.addr == "" {
		t.Error("expected non-empty address")
	}
}

func TestNewServer_InvalidPort(t *testing.T) {
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, nil
	}

	// Use a port that's already in use by binding first
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	_, err = NewServer(dial, port)
	if err == nil {
		t.Fatal("expected error when port is already in use")
	}
}

func TestServer_Close(t *testing.T) {
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, nil
	}

	srv, err := NewServer(dial, 0)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Closing again should fail (listener already closed)
	if err := srv.Close(); err == nil {
		t.Error("expected error on double close")
	}
}

func TestServer_Start(t *testing.T) {
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, nil
	}

	srv, err := NewServer(dial, 0)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()

	// Start should not panic
	srv.Start()
}

func TestServer_SOCKS5ProxyConnection(t *testing.T) {
	// Set up a target TCP server that echoes data back
	targetLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start target server: %v", err)
	}
	defer targetLn.Close()

	go func() {
		for {
			conn, err := targetLn.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				io.Copy(conn, conn) // echo
			}()
		}
	}()

	// Track that our dialer was actually invoked
	dialerCalled := false
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialerCalled = true
		return net.Dial(network, addr)
	}

	srv, err := NewServer(dial, 0)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()
	srv.Start()

	// Connect through the SOCKS5 proxy to the echo server
	dialer, err := proxy.SOCKS5("tcp", srv.addr, nil, proxy.Direct)
	if err != nil {
		t.Fatalf("SOCKS5 dialer: %v", err)
	}

	conn, err := dialer.Dial("tcp", targetLn.Addr().String())
	if err != nil {
		t.Fatalf("Dial through proxy: %v", err)
	}
	defer conn.Close()

	// Send data and verify echo
	msg := []byte("hello from socks test")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("Read: %v", err)
	}

	if string(buf) != string(msg) {
		t.Errorf("got %q, want %q", buf, msg)
	}

	if !dialerCalled {
		t.Error("expected custom dialer to be called")
	}
}

func TestServer_SOCKS5_DialerError(t *testing.T) {
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, fmt.Errorf("connection refused")
	}

	srv, err := NewServer(dial, 0)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()
	srv.Start()

	dialer, err := proxy.SOCKS5("tcp", srv.addr, nil, proxy.Direct)
	if err != nil {
		t.Fatalf("SOCKS5 dialer: %v", err)
	}

	// Dial should fail because our dialer returns an error
	_, err = dialer.Dial("tcp", "example.com:80")
	if err == nil {
		t.Fatal("expected error from dialer failure")
	}
}
