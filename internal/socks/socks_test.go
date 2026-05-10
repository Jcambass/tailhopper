package socks

import (
	"context"
	"fmt"
	"net"
	"testing"
)

func TestNewServer_BindsPort(t *testing.T) {
	dialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, fmt.Errorf("not implemented")
	}

	s, err := NewServer(dialer, 0)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer s.Close()

	if s.addr == "" {
		t.Error("expected non-empty address")
	}
}

func TestNewServer_PortInUse(t *testing.T) {
	// Bind a port first
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	dialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, nil
	}

	_, err = NewServer(dialer, port)
	if err == nil {
		t.Fatal("expected error when port is in use")
	}
}

func TestServer_Close(t *testing.T) {
	dialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, nil
	}

	s, err := NewServer(dialer, 0)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestServer_StartAndClose(t *testing.T) {
	dialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, fmt.Errorf("not implemented")
	}

	s, err := NewServer(dialer, 0)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	s.Start()

	if err := s.Close(); err != nil {
		t.Fatalf("Close after Start: %v", err)
	}
}
