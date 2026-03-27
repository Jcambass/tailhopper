package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/jcambass/tailhopper/internal/sse"
	"github.com/jcambass/tailhopper/internal/tailscale"
	tsnetpkg "github.com/jcambass/tailhopper/internal/tsnet"
	"tailscale.com/util/dnsname"
)

type PersistedTailnet struct {
	// ID is a unique stable identifier for the tailnet.
	ID int `json:"id"`
	// StateDir is the path to the directory where the tailnet stores its state.
	StateDir string `json:"state_dir"`
	// SocksPort is the port on which the SOCKS5 proxy listens.
	SocksPort int `json:"socks_port"`
	// Hostname is the hostname used for the tailnet device.
	Hostname string `json:"hostname"`
	// UserEnabled records whether the user last switched the tailnet on.
	UserEnabled bool `json:"user_enabled"`
	// ClaimedMagicDNSSuffix is the domain we expect this tailnet to be logged into.
	// If empty, it will be set upon first successful connection.
	ClaimedMagicDNSSuffix string `json:"claimed_magic_dns_suffix,omitempty"`
	// TerminalError stores a fatal error that prevents the tailnet from starting.
	TerminalError string `json:"terminal_error,omitempty"`
}

type RegisteredTailnet struct {
	*tailscale.Tailnet
	// config is the persisted configuration for this tailnet.
	config PersistedTailnet
}

type Registry struct {
	path        string
	mu          sync.RWMutex
	nextID      int
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
			return &tailscale.AlreadyClaimedError{Suffix: suffix}
		}
	}

	tailnet, ok := m.tailnets[tailnetID]
	if !ok {
		return fmt.Errorf("tailnet not found")
	}

	// Update the config for this tailnet
	tailnet.config.ClaimedMagicDNSSuffix = suffix

	// Persist the change to disk
	if err := m.saveLocked(); err != nil {
		return err
	}

	// Setting the claimed suffix on the tailnet instance itself is done in the caller after a successful claim.
	// Notifying about the state change for that tailnet specifically is also done in the caller.

	if m.broadcaster != nil {
		// Notify globally since per definition, we now have no more unconfigured tailnets
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
		tailnet := tailscale.NewTailnet(c.ID, c.StateDir, c.Hostname, c.ClaimedMagicDNSSuffix, c.TerminalError, c.UserEnabled, c.SocksPort, m, tsnetpkg.NewRealTSNetServer)
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

// OnChange persists relevant fields from the snapshot and broadcasts the change.
func (m *Registry) OnChange(snapshot tailscale.TailnetSnapshot) {
	m.mu.Lock()

	tailnet, ok := m.tailnets[snapshot.ID]
	if ok {
		tailnet.config.UserEnabled = snapshot.UserState == tailscale.UserEnabled
		tailnet.config.TerminalError = snapshot.TerminalError
		_ = m.saveLocked()
	}

	m.mu.Unlock()

	if m.broadcaster != nil {
		m.broadcaster.BroadcastTailnetChange(snapshot.ID)
	}
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
func (m *Registry) List() []*tailscale.Tailnet {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var tailnets []*tailscale.Tailnet
	for _, tailnet := range m.tailnets {
		tailnets = append(tailnets, tailnet.Tailnet)
	}

	// Return tailnets in sorted order by numeric ID for consistency.
	sort.Slice(tailnets, func(i, j int) bool {
		return tailnets[i].ID() < tailnets[j].ID()
	})

	return tailnets
}

// RestoreEnabledTailnets starts tailnets that were user-enabled before shutdown.
//
// We only auto-start tailnets that are currently in StoppedState to avoid
// interfering with terminal/error states restored from persisted config.
func (m *Registry) RestoreEnabledTailnets(ctx context.Context) {
	tailnets := m.List()

	for _, tailnet := range tailnets {
		snapshot := tailnet.Snapshot()
		if snapshot.UserState != tailscale.UserEnabled {
			continue
		}

		if snapshot.State != tailscale.StoppedState {
			continue
		}

		slog.InfoContext(ctx, "restoring enabled tailnet",
			slog.String("component", "registry"),
			slog.Int("tailnet_id", tailnet.ID()),
		)

		if err := tailnet.Start(ctx); err != nil {
			slog.ErrorContext(ctx, "failed to restore enabled tailnet",
				slog.String("component", "registry"),
				slog.Int("tailnet_id", tailnet.ID()),
				slog.Any("error", err),
			)
		}
	}
}

// Add creates a new unconfigured tailnet with the given hostname and returns it.
// If hostname is empty, a default one will be generated based on the machine's hostname.
// Example: if the machine's hostname is "laptop", the generated hostname will be "laptop-tailhopper".
func (m *Registry) Add(hostname string) (*tailscale.Tailnet, error) {
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

	// New tailnets start disabled; user_enabled will be persisted when the user starts it.
	tailnet := tailscale.NewTailnet(c.ID, c.StateDir, c.Hostname, "", "", false, c.SocksPort, m, tsnetpkg.NewRealTSNetServer)

	m.tailnets[c.ID] = &RegisteredTailnet{
		Tailnet: tailnet,
		config:  c,
	}

	// Rollback on save failure
	if err := m.saveLocked(); err != nil {
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

	if tailnet.config.StateDir != "" {
		// Delete the state directory from disk
		if err := os.RemoveAll(tailnet.config.StateDir); err != nil {
			slog.Error("failed to remove state directory", slog.String("component", "registry"), slog.String("dir", tailnet.config.StateDir), slog.Any("error", err))
			// Continue with deletion even if directory removal fails
		}
	}

	delete(m.tailnets, id)

	if err := m.saveLocked(); err != nil {
		// If save fails, we're in an inconsistent state in memory vs disk.
		// But the object is gone from memory. This is a best effort.
	}

	// Notify about global change (tailnet deleted)
	if m.broadcaster != nil {
		m.broadcaster.BroadcastGlobalChange()
	}

	return nil
}

// Get retrieves a tailnet by ID. The boolean indicates if the tailnet was found.
func (m *Registry) Get(id int) (*tailscale.Tailnet, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, ok := m.tailnets[id]
	if !ok {
		return nil, false
	}
	return t.Tailnet, ok
}

// HasUnconfiguredTailnets returns true if any tailnet hasn't been configured (no MagicDNS suffix claimed) yet.
func (m *Registry) HasUnconfiguredTailnets() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, tailnet := range m.tailnets {
		if tailnet.config.ClaimedMagicDNSSuffix == "" {
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
