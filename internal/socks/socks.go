// Package socks provides a SOCKS5 proxy server.
package socks

import (
	"context"
	"log"
	"net"

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

// NewServer creates a new SOCKS5 server.
func NewServer(addr string, dial Dialer) (*Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	return &Server{
		server: &socks5.Server{
			Dialer: dial,
		},
		listener: listener,
		addr:     addr,
	}, nil
}

// Addr returns the address the server is listening on.
func (s *Server) Addr() string {
	return s.addr
}

// Start begins serving SOCKS5 connections in the background.
func (s *Server) Start() {
	go func() {
		if err := s.server.Serve(s.listener); err != nil {
			log.Printf("SOCKS5 server error: %v", err)
		}
	}()
	log.Printf("SOCKS5 proxy listening on %s", s.addr)
}

// Close stops the SOCKS5 server.
func (s *Server) Close() error {
	return s.listener.Close()
}
