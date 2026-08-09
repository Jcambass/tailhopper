// Package socks provides a SOCKS5 proxy server.
package socks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/jcambass/tailhopper/internal/logging"
	"tailscale.com/net/socks5"
)

// Server is a SOCKS5 proxy server.
type Server struct {
	server   *socks5.Server
	listener net.Listener
	addr     string
	mu       sync.Mutex
	done     chan struct{}
}

// NewServer creates a new SOCKS5 server on the specified port.
func NewServer(dial func(context.Context, string, string) (net.Conn, error), port int) (*Server, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	// Wrap the dialer with logging
	loggingDialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		start := time.Now()
		slog.DebugContext(ctx, "SOCKS5 connection attempt",
			slog.String("component", "socksserver"),
			slog.String("network", network),
			slog.String("dest", addr))

		conn, err := dial(ctx, network, addr)
		duration := time.Since(start)

		if err != nil {
			slog.ErrorContext(ctx, "SOCKS5 connection failed",
				slog.String("component", "socksserver"),
				slog.String("network", network),
				slog.String("dest", addr),
				slog.Duration("duration", duration),
				slog.Any("error", err))
			return nil, err
		}

		slog.DebugContext(ctx, "SOCKS5 connection established",
			slog.String("component", "socksserver"),
			slog.String("network", network),
			slog.String("dest", addr),
			slog.Duration("duration", duration))
		return conn, nil
	}

	return &Server{
		server: &socks5.Server{
			Dialer: loggingDialer,
		},
		listener: listener,
		addr:     listener.Addr().String(),
	}, nil
}

// Start begins serving SOCKS5 connections in the background.
func (s *Server) Start() {
	s.mu.Lock()
	if s.done != nil {
		s.mu.Unlock()
		return
	}
	done := make(chan struct{})
	s.done = done
	s.mu.Unlock()

	ctx := context.Background()
	go func() {
		defer close(done)
		defer logging.CatchPanic(ctx)
		if err := s.server.Serve(s.listener); err != nil && !errors.Is(err, net.ErrClosed) {
			slog.ErrorContext(ctx, "SOCKS5 server error", slog.String("component", "socksserver"), slog.Any("error", err))
		}
	}()
	slog.Info("SOCKS5 proxy listening", slog.String("component", "socksserver"), slog.String("addr", s.addr))
}

// Close stops the SOCKS5 server.
func (s *Server) Close() error {
	err := s.listener.Close()
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	return err
}
