// Package portscan provides port scanning functionality for Tailnet machines.
package portscan

import (
	"context"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Dialer is a function that dials a network connection.
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

// PortInfo represents an open port.
type PortInfo struct {
	Port  uint16
	Label string
}

// Cache stores scan results and state.
type Cache struct {
	results map[string][]PortInfo
	state   map[string]int
	mu      sync.RWMutex
}

// NewCache creates a new scan cache.
func NewCache() *Cache {
	return &Cache{
		results: make(map[string][]PortInfo),
		state:   make(map[string]int),
	}
}

// Get retrieves cached ports for a machine by checking multiple name variants.
func (c *Cache) Get(names ...string) (ports []PortInfo, found bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, name := range names {
		if services, ok := c.results[name]; ok {
			return sortPorts(services), true
		}
	}
	return nil, false
}

// Set stores scan results for a machine.
func (c *Cache) Set(machine string, ports []PortInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results[machine] = ports
}

// IsScanning checks if any of the given names are currently being scanned.
func (c *Cache) IsScanning(names ...string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, name := range names {
		if count, ok := c.state[name]; ok && count > 0 {
			return true
		}
	}
	return false
}

// StartScan marks a machine as being scanned.
func (c *Cache) StartScan(machine string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state[machine]++
}

// FinishScan marks a machine scan as complete.
func (c *Cache) FinishScan(machine string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if count, ok := c.state[machine]; ok {
		if count <= 1 {
			delete(c.state, machine)
		} else {
			c.state[machine] = count - 1
		}
	}
}

// ScanHost scans a host for open ports using the provided dialer.
func ScanHost(ctx context.Context, host string, ports []int, dialer Dialer) []PortInfo {
	type result struct {
		port int
		open bool
	}

	results := make(chan result, len(ports))
	var wg sync.WaitGroup

	// Limit concurrent scans per host
	sem := make(chan struct{}, 50)

	for _, port := range ports {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				results <- result{port: p, open: false}
				return
			case sem <- struct{}{}:
				defer func() { <-sem }()
			}

			// Create a timeout context for this port dial
			dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()

			addr := net.JoinHostPort(host, strconv.Itoa(p))
			conn, err := dialer(dialCtx, "tcp", addr)
			if err == nil {
				conn.Close()
				results <- result{port: p, open: true}
			} else {
				results <- result{port: p, open: false}
			}
		}(port)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var openPorts []PortInfo
	for res := range results {
		if res.open {
			openPorts = append(openPorts, PortInfo{
				Port:  uint16(res.port),
				Label: strconv.Itoa(res.port),
			})
		}
	}
	return openPorts
}

// GeneratePortRange creates a slice of port numbers from start to end (inclusive).
func GeneratePortRange(start, end int) []int {
	ports := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		ports = append(ports, i)
	}
	return ports
}

// CommonHTTPPorts returns the default range of ports to scan (20-9999).
func CommonHTTPPorts() []int {
	return GeneratePortRange(20, 9999)
}

// SortPorts sorts ports by port number.
func sortPorts(ports []PortInfo) []PortInfo {
	if len(ports) == 0 {
		return nil
	}

	sorted := make([]PortInfo, len(ports))
	copy(sorted, ports)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Port < sorted[j].Port
	})
	return sorted
}

// SortPorts is the exported version that sorts ports by port number.
func SortPorts(ports []PortInfo) []PortInfo {
	return sortPorts(ports)
}
