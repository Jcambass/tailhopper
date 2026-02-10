// Package socks provides a SOCKS5 proxy server.
package socks

import (
	"context"
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
	logger   *logging.Logger
}

// NewServer creates a new SOCKS5 server.
func NewServer(dial Dialer) (*Server, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	logger := logging.Default().With("component", "socksserver")

	return &Server{
		server: &socks5.Server{
			Dialer: dial,
		},
		listener: listener,
		addr:     listener.Addr().String(),
		logger:   logger,
	}, nil
}

// Addr returns the address the server is listening on.
func (s *Server) Addr() string {
	return s.addr
}

// Start begins serving SOCKS5 connections in the background.
func (s *Server) Start() {
	go func() {
		defer logging.CatchPanic(s.logger)
		if err := s.server.Serve(s.listener); err != nil {
			s.logger.Printf("SOCKS5 server error: %v", err)
		}
	}()
	s.logger.Printf("SOCKS5 proxy listening on %s", s.addr)
}

// Close stops the SOCKS5 server.
func (s *Server) Close() error {
	return s.listener.Close()
}
