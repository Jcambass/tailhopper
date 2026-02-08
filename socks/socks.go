// Package socks provides a SOCKS5 proxy server with connection tracking.
package socks

import (
	"context"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"tailscale.com/net/socks5"
)

// Dialer is a function that dials a network connection.
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

// ConnectionEntry represents a completed or failed connection.
type ConnectionEntry struct {
	Host      string
	Port      string
	StartTime time.Time
	EndTime   time.Time
	BytesSent int64
	BytesRecv int64
	Error     string
	Connected bool // true if dial succeeded (for live connections)
}

// liveConnection tracks an active connection with its byte counts.
type liveConnection struct {
	Host      string
	Port      string
	StartTime time.Time
	BytesSent atomic.Int64
	BytesRecv atomic.Int64
	Connected atomic.Bool // true once dial succeeds
}

// ConnectionLog tracks proxy connections.
type ConnectionLog struct {
	mu          sync.RWMutex
	connections []ConnectionEntry
	live        map[*liveConnection]struct{}
	maxEntries  int
}

// NewConnectionLog creates a new connection log.
func NewConnectionLog(maxEntries int) *ConnectionLog {
	return &ConnectionLog{
		connections: make([]ConnectionEntry, 0, maxEntries),
		live:        make(map[*liveConnection]struct{}),
		maxEntries:  maxEntries,
	}
}

func (cl *ConnectionLog) startConnection(host, port string) *liveConnection {
	lc := &liveConnection{
		Host:      host,
		Port:      port,
		StartTime: time.Now(),
	}
	cl.mu.Lock()
	cl.live[lc] = struct{}{}
	cl.mu.Unlock()
	return lc
}

func (cl *ConnectionLog) endConnection(lc *liveConnection, err error) {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	if _, ok := cl.live[lc]; !ok {
		return
	}
	delete(cl.live, lc)

	connEntry := ConnectionEntry{
		Host:      lc.Host,
		Port:      lc.Port,
		StartTime: lc.StartTime,
		EndTime:   time.Now(),
		BytesSent: lc.BytesSent.Load(),
		BytesRecv: lc.BytesRecv.Load(),
	}
	if err != nil {
		connEntry.Error = err.Error()
	}

	cl.connections = append(cl.connections, connEntry)
	if len(cl.connections) > cl.maxEntries {
		cl.connections = cl.connections[1:]
	}
}

// ActiveCount returns the number of currently live connections.
func (cl *ConnectionLog) ActiveCount() int {
	cl.mu.RLock()
	defer cl.mu.RUnlock()
	return len(cl.live)
}

// GetRecent returns recent connections (newest first) and live connections.
// GetRecent returns recent connections (newest first) and live connections.
func (cl *ConnectionLog) GetRecent(n int) ([]ConnectionEntry, []ConnectionEntry) {
	cl.mu.RLock()
	defer cl.mu.RUnlock()

	// Get recent completed connections (reversed for newest first)
	start := len(cl.connections) - n
	if start < 0 {
		start = 0
	}
	recent := make([]ConnectionEntry, 0, n)
	for i := len(cl.connections) - 1; i >= start; i-- {
		recent = append(recent, cl.connections[i])
	}

	// Get live connections
	live := make([]ConnectionEntry, 0, len(cl.live))
	for lc := range cl.live {
		live = append(live, ConnectionEntry{
			Host:      lc.Host,
			Port:      lc.Port,
			StartTime: lc.StartTime,
			BytesSent: lc.BytesSent.Load(),
			BytesRecv: lc.BytesRecv.Load(),
			Connected: lc.Connected.Load(),
		})
	}

	return recent, live
}

// trackedConn wraps a net.Conn to track bytes transferred.
type trackedConn struct {
	net.Conn
	lc      *liveConnection
	connLog *ConnectionLog
	closed  atomic.Bool
}

func (tc *trackedConn) Read(b []byte) (int, error) {
	n, err := tc.Conn.Read(b)
	if n > 0 {
		tc.lc.BytesRecv.Add(int64(n))
	}
	return n, err
}

func (tc *trackedConn) Write(b []byte) (int, error) {
	n, err := tc.Conn.Write(b)
	if n > 0 {
		tc.lc.BytesSent.Add(int64(n))
	}
	return n, err
}

func (tc *trackedConn) Close() error {
	if tc.closed.CompareAndSwap(false, true) {
		tc.connLog.endConnection(tc.lc, nil)
	}
	return tc.Conn.Close()
}

// wrapDialer wraps a dialer function to track connections.
func wrapDialer(dial Dialer, connLog *ConnectionLog) Dialer {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, _ := net.SplitHostPort(addr)
		lc := connLog.startConnection(host, port)

		conn, err := dial(ctx, network, addr)
		if err != nil {
			connLog.endConnection(lc, err)
			return nil, err
		}

		lc.Connected.Store(true)

		return &trackedConn{
			Conn:    conn,
			lc:      lc,
			connLog: connLog,
		}, nil
	}
}

// DefaultMaxLogEntries is the default number of connection entries to keep.
const DefaultMaxLogEntries = 100

// Server is a SOCKS5 proxy server with connection tracking.
type Server struct {
	server   *socks5.Server
	listener net.Listener
	addr     string
	// ConnLog tracks active and completed connections.
	ConnLog *ConnectionLog
}

// NewServer creates a new SOCKS5 server with connection tracking.
func NewServer(addr string, dial Dialer) (*Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	connLog := NewConnectionLog(DefaultMaxLogEntries)

	// Wrap the dialer with connection tracking
	trackedDial := wrapDialer(dial, connLog)

	return &Server{
		server: &socks5.Server{
			Dialer: trackedDial,
		},
		listener: listener,
		addr:     addr,
		ConnLog:  connLog,
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
