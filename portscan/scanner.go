// Package portscan provides port scanning functionality for Tailnet machines.
package portscan

import (
	"context"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"
)

// Dialer is a function that dials a network connection.
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

// Scanner manages port scanning state and results for multiple hosts.
type Scanner struct {
	Dialer      Dialer
	results     map[string][]int
	scanning    map[string]bool
	mu          sync.RWMutex
	portsToScan []int
}

// NewScanner creates a new Scanner with the given dialer.
func NewScanner(dialer Dialer) *Scanner {
	return &Scanner{
		Dialer:      dialer,
		results:     make(map[string][]int),
		scanning:    make(map[string]bool),
		portsToScan: generatePortRange(20, 9999),
	}
}

func (s *Scanner) Scan(ctx context.Context, host string) []int {
	s.mu.Lock()
	if s.scanning[host] {
		s.mu.Unlock()
		return nil // Already scanning this host
	}
	s.scanning[host] = true
	// clear the cache for this host before scanning
	delete(s.results, host)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.scanning[host] = false
		s.mu.Unlock()
	}()

	results := make(chan int, len(s.portsToScan))
	var wg sync.WaitGroup

	// Limit concurrent scans per host
	sem := make(chan struct{}, 50)

	for _, port := range s.portsToScan {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()

			select {
			case <-ctx.Done():

				return
			case sem <- struct{}{}:
				defer func() { <-sem }()
			}

			// Create a timeout context for this port dial
			dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()

			addr := net.JoinHostPort(host, strconv.Itoa(p))
			conn, err := s.Dialer(dialCtx, "tcp", addr)
			if err == nil {
				conn.Close()
				results <- p
			}
		}(port)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var openPorts []int
	for port := range results {
		openPorts = append(openPorts, port)
	}

	slices.Sort(openPorts)

	// Cache results
	s.mu.Lock()
	s.results[host] = openPorts
	s.mu.Unlock()

	return openPorts
}

func (s *Scanner) GetCachedResults(host string) ([]int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ports, found := s.results[host]
	return ports, found
}

func (s *Scanner) IsScanning(host string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scanning[host]
}

// generatePortRange creates a slice of port numbers from start to end (inclusive).
func generatePortRange(start, end int) []int {
	ports := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		ports = append(ports, i)
	}
	return ports
}
