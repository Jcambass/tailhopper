package ts

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/jcambass/tailhopper/internal/sse"
	"tailscale.com/util/dnsname"
)

// MagicDNSSuffixRegistry defines the interface for claiming MagicDNS suffixes for tailnets.
// This helps to ensure that no two tailnets claim the same MagicDNS suffix.
type MagicDNSSuffixRegistry interface {
	// Claim attempts to claim the given MagicDNS suffix for the specified tailnet ID.
	// It returns an error if the suffix is already claimed by another tailnet or if there is a mismatch with an existing claim.
	Claim(tailnetID int, suffix string) error
}

type AlreadyClaimedError struct {
	Suffix string
}

func (e *AlreadyClaimedError) Error() string {
	return fmt.Sprintf("magic DNS suffix '%s' is already claimed by another tailnet", e.Suffix)
}

type PersistedTailnet struct {
	// ID is a unique stable identifier for the tailnet.
	ID int `json:"id"`
	// StateDir is the path to the directory where the tailnet stores its state.
	StateDir string `json:"state_dir"`
	// SocksPort is the port on which the SOCKS5 proxy listens.
	SocksPort int `json:"socks_port"`
	// Hostname is the hostname used for the tailnet device.
	Hostname string `json:"hostname"`
	// ClaimedMagicDNSSuffix is the domain we expect this tailnet to be logged into.
	// If empty, it will be set upon first successful connection.
	ClaimedMagicDNSSuffix string `json:"claimed_magic_dns_suffix,omitempty"`
	// TerminalError stores a fatal error that prevents the tailnet from starting.
	TerminalError string `json:"terminal_error,omitempty"`
}

type RegisteredTailnet struct {
	*Tailnet
	// config is the persisted configuration for this tailnet.
	config PersistedTailnet
}

type Registry struct {
	path   string
	mu     sync.RWMutex
	nextID int

	tailnets    map[int]*RegisteredTailnet
	broadcaster sse.Broadcaster
}

func NewRegistry(path string, broadcaster sse.Broadcaster) (*Registry, error) {
	m := &Registry{
		path:        path,
		nextID:      1,
		tailnets:    make(map[int]*RegisteredTailnet),
		broadcaster: broadcaster,
	}
	if err := m.Load(); err != nil {
		if os.IsNotExist(err) {
			// It's okay if the file doesn't exist yet
			return m, nil
		}
		return nil, err
	}
	return m, nil
}

func (m *Registry) Claim(tailnetID int, suffix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if the suffix is already claimed by another tailnet
	for _, tailnet := range m.tailnets {
		if tailnet.config.ClaimedMagicDNSSuffix == suffix {
			return &AlreadyClaimedError{Suffix: suffix}
		}
	}

	// Update the config for this tailnet
	tailnet, ok := m.tailnets[tailnetID]
	if !ok {
		return fmt.Errorf("tailnet not found")
	}
	tailnet.config.ClaimedMagicDNSSuffix = suffix

	// Setting the claimed suffix on the tailnet instance itself is done in the caller after a successful claim.
	// Notifying about the state change for that tailnet specifically is also done in the caller.

	// Persist the change to disk
	if err := m.saveLocked(); err != nil {
		return err
	}

	// Notify globally since per definition, we now have no more unconfigured tailnets
	if m.broadcaster != nil {
		m.broadcaster.BroadcastGlobalChange()
	}

	return nil
}

// Load reads the config file and initializes the in-memory state.
func (m *Registry) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, err := os.Open(m.path)
	if err != nil {
		return err
	}
	defer f.Close()

	var list []PersistedTailnet
	if err := json.NewDecoder(f).Decode(&list); err != nil {
		return err
	}

	m.tailnets = make(map[int]*RegisteredTailnet)
	m.nextID = 1
	for _, c := range list {
		broadcast := func() {
			// Notify about the change for this tailnet
			if m.broadcaster != nil {
				m.broadcaster.BroadcastTailnetChange(c.ID)
			}
		}
		tailnet := NewTailnet(c.ID, c.StateDir, c.Hostname, c.ClaimedMagicDNSSuffix, c.TerminalError, c.SocksPort, m, broadcast)
		m.tailnets[c.ID] = &RegisteredTailnet{
			Tailnet: tailnet,
			config:  c,
		}
		// Update nextID based on loaded IDs
		if c.ID >= m.nextID {
			m.nextID = c.ID + 1
		}
	}

	return nil
}

func (m *Registry) saveLocked() error {
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	f, err := os.Create(m.path)
	if err != nil {
		return err
	}
	defer f.Close()

	list := make([]PersistedTailnet, 0, len(m.tailnets))
	for _, tailnet := range m.tailnets {
		list = append(list, tailnet.config)
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

// List returns all tailnets in the registry, sorted by their numeric ID.
func (m *Registry) List() []*Tailnet {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// Return tailnets in sorted order by numeric ID for consistency.
	var tailnets []*Tailnet
	for _, tailnet := range m.tailnets {
		tailnets = append(tailnets, tailnet.Tailnet)
	}

	sort.Slice(tailnets, func(i, j int) bool {
		return tailnets[i].ID() < tailnets[j].ID()
	})

	return tailnets
}

// Add creates a new unconfigured tailnet with the given hostname and returns it.
// If hostname is empty, a default one will be generated based on the machine's hostname.
// Example: if the machine's hostname is "laptop", the generated hostname will be "laptop-tailhopper".
func (m *Registry) Add(hostname string) (*Tailnet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := m.nextID
	m.nextID++
	stateDir := filepath.Join(filepath.Dir(m.path), "tailnets", fmt.Sprintf("%d", id))

	// Generate hostname if not provided
	if hostname == "" {
		if realHostname, err := os.Hostname(); err == nil {
			// Use Tailscale's hostname sanitization logic
			hostname = dnsname.SanitizeHostname(realHostname) + "-tailhopper"
		} else {
			hostname = "tailhopper"
		}
	}

	// Find an available port for SOCKS proxy
	socksPort, err := findAvailablePort()
	if err != nil {
		return nil, fmt.Errorf("failed to find available port: %w", err)
	}

	c := PersistedTailnet{
		ID:        id,
		StateDir:  stateDir,
		Hostname:  hostname,
		SocksPort: socksPort,
	}

	broadcast := func() {
		// Notify about the change for this tailnet
		if m.broadcaster != nil {
			m.broadcaster.BroadcastTailnetChange(id)
		}
	}

	tailnet := NewTailnet(c.ID, c.StateDir, c.Hostname, "", "", c.SocksPort, m, broadcast)
	m.tailnets[c.ID] = &RegisteredTailnet{
		Tailnet: tailnet,
		config:  c,
	}

	if err := m.saveLocked(); err != nil {
		// Rollback on save failure
		delete(m.tailnets, c.ID)
		return nil, err
	}

	// Notify about global change (new tailnet added)
	if m.broadcaster != nil {
		m.broadcaster.BroadcastGlobalChange()
	}

	return tailnet, nil
}

// Delete removes a tailnet from the registry and deletes its state directory from disk.
func (m *Registry) Delete(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tailnet, exists := m.tailnets[id]
	if !exists {
		return fmt.Errorf("tailnet not found")
	}

	// Delete the state directory from disk
	if tailnet.config.StateDir != "" {
		if err := os.RemoveAll(tailnet.config.StateDir); err != nil {
			slog.Warn("failed to remove state directory", "component", "registry", "dir", tailnet.config.StateDir, "error", err)
			// Continue with deletion even if directory removal fails
		}
	}

	delete(m.tailnets, id)

	err := m.saveLocked()
	if err == nil && m.broadcaster != nil {
		// Notify about global change (tailnet deleted)
		m.broadcaster.BroadcastGlobalChange()
	}

	return err
}

// Get retrieves a tailnet by ID. The boolean indicates if the tailnet was found.
func (m *Registry) Get(id int) (*Tailnet, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tailnets[id]
	return t.Tailnet, ok
}

// HasUnconfiguredTailnets returns true if any tailnet hasn't been configured (no MagicDNS suffix claimed) yet.
func (m *Registry) HasUnconfiguredTailnets() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, tailnet := range m.tailnets {
		if tailnet.claimedMagicDNSSuffix == "" {
			return true
		}
	}
	return false
}

// findAvailablePort finds an available port by temporarily binding to 127.0.0.1:0.
func findAvailablePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	addr := listener.Addr().(*net.TCPAddr)
	return addr.Port, nil
}
