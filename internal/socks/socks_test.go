package socks

import (
	"context"
	"net"
	"testing"
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
