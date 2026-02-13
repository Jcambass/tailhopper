// Package socks provides a SOCKS5 proxy server.
package socks

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/jcambass/tailhopper/internal/logging"
	"tailscale.com/net/socks5"
)

// Dialer is a function that dials a network connection.
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

// Server is a SOCKS5 proxy server.
type Server struct {
	server   *socks5.Server
	listener net.Listener
	addr     string
}

// NewServer creates a new SOCKS5 server on the specified port.
func NewServer(dial Dialer, port int) (*Server, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	return &Server{
		server: &socks5.Server{
			Dialer: dial,
		},
		listener: listener,
		addr:     listener.Addr().String(),
	}, nil
}

// Start begins serving SOCKS5 connections in the background.
func (s *Server) Start() {
	ctx := context.Background()
	go func() {
		defer logging.CatchPanic(ctx)
		if err := s.server.Serve(s.listener); err != nil {
			slog.ErrorContext(ctx, "SOCKS5 server error", "component", "socksserver", "error", err)
		}
	}()
	slog.Info("SOCKS5 proxy listening", "component", "socksserver", "addr", s.addr)
}

// Close stops the SOCKS5 server.
func (s *Server) Close() error {
	return s.listener.Close()
}
