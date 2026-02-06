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

// LiveConnection tracks an active connection's byte counts.
type LiveConnection struct {
	BytesSent atomic.Int64
	BytesRecv atomic.Int64
}

// ConnectionTracker tracks active and completed connections.
type ConnectionTracker interface {
	StartConnection(host, port string, lc *LiveConnection)
	EndConnection(lc *LiveConnection, err error)
}

// ConnectionEntry represents a completed or failed connection.
type ConnectionEntry struct {
	Host      string
	Port      string
	StartTime time.Time
	EndTime   time.Time
	BytesSent int64
	BytesRecv int64
	Error     string
}

// liveConnectionEntry tracks an active connection with its LiveConnection.
type liveConnectionEntry struct {
	Host      string
	Port      string
	StartTime time.Time
	lc        *LiveConnection
}

// ConnectionLog tracks proxy connections.
// It implements ConnectionTracker.
type ConnectionLog struct {
	mu          sync.RWMutex
	connections []ConnectionEntry
	live        map[*LiveConnection]*liveConnectionEntry
	maxEntries  int
}

// NewConnectionLog creates a new connection log.
func NewConnectionLog(maxEntries int) *ConnectionLog {
	return &ConnectionLog{
		connections: make([]ConnectionEntry, 0, maxEntries),
		live:        make(map[*LiveConnection]*liveConnectionEntry),
		maxEntries:  maxEntries,
	}
}

// StartConnection records a new connection attempt.
// Implements ConnectionTracker.
func (cl *ConnectionLog) StartConnection(host, port string, lc *LiveConnection) {
	entry := &liveConnectionEntry{
		Host:      host,
		Port:      port,
		StartTime: time.Now(),
		lc:        lc,
	}
	cl.mu.Lock()
	cl.live[lc] = entry
	cl.mu.Unlock()
}

// EndConnection marks a connection as complete.
// Implements ConnectionTracker.
func (cl *ConnectionLog) EndConnection(lc *LiveConnection, err error) {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	entry, ok := cl.live[lc]
	if !ok {
		return
	}
	delete(cl.live, lc)

	connEntry := ConnectionEntry{
		Host:      entry.Host,
		Port:      entry.Port,
		StartTime: entry.StartTime,
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
	for lc, entry := range cl.live {
		live = append(live, ConnectionEntry{
			Host:      entry.Host,
			Port:      entry.Port,
			StartTime: entry.StartTime,
			BytesSent: lc.BytesSent.Load(),
			BytesRecv: lc.BytesRecv.Load(),
		})
	}

	return recent, live
}

// TrackedConn wraps a net.Conn to track bytes transferred.
type TrackedConn struct {
	net.Conn
	lc *LiveConnection
}

func (tc *TrackedConn) Read(b []byte) (int, error) {
	n, err := tc.Conn.Read(b)
	if n > 0 {
		tc.lc.BytesRecv.Add(int64(n))
	}
	return n, err
}

func (tc *TrackedConn) Write(b []byte) (int, error) {
	n, err := tc.Conn.Write(b)
	if n > 0 {
		tc.lc.BytesSent.Add(int64(n))
	}
	return n, err
}

type trackedConnWithClose struct {
	TrackedConn
	tracker ConnectionTracker
	closed  atomic.Bool
}

func (tc *trackedConnWithClose) Close() error {
	if tc.closed.CompareAndSwap(false, true) {
		tc.tracker.EndConnection(tc.lc, nil)
	}
	return tc.Conn.Close()
}

// wrapDialer wraps a dialer function to track connections.
func wrapDialer(dial Dialer, tracker ConnectionTracker) Dialer {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, _ := net.SplitHostPort(addr)
		lc := &LiveConnection{}
		tracker.StartConnection(host, port, lc)

		conn, err := dial(ctx, network, addr)
		if err != nil {
			tracker.EndConnection(lc, err)
			return nil, err
		}

		return &trackedConnWithClose{
			TrackedConn: TrackedConn{Conn: conn, lc: lc},
			tracker:     tracker,
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
