package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/jcambass/tailhopper/internal/tailscale"
	"tailscale.com/util/dnsname"
)

type TailnetConfig struct {
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
}

var (
	ErrTailnetNotFound     = errors.New("tailnet not found")
	ErrUnconfiguredTailnet = errors.New("an unconfigured tailnet already exists")
)

type TailnetView struct {
	ID                 int
	ConfiguredHostname string
	NodeHostname       string
	MagicDNSSuffix     string
	SocksPort          int
	UserEnabled        bool
	Started            bool
	BackendState       string
	AuthURL            string
	Peers              []Peer
	Error              string
}

type Peer struct {
	Name      string
	DNSName   string
	Online    bool
	Addresses []netip.Prefix
}

func (v TailnetView) EffectiveHostname() string {
	if v.NodeHostname != "" {
		return v.NodeHostname
	}
	return v.ConfiguredHostname
}

func (v TailnetView) SocksAddr() string {
	return fmt.Sprintf("localhost:%d", v.SocksPort)
}

type tailnetEntry struct {
	op      sync.Mutex
	config  TailnetConfig
	runtime runtime
}

type runtime interface {
	Start(context.Context) error
	Stop(context.Context) error
	Info(context.Context) tailscale.Info
}

type Registry struct {
	path    string
	mu      sync.RWMutex
	nextID  int
	entries map[int]*tailnetEntry
}

func NewRegistry(path string) (*Registry, error) {
	m := &Registry{
		path:    path,
		nextID:  1,
		entries: make(map[int]*tailnetEntry),
	}

	if err := m.load(); err != nil {
		if os.IsNotExist(err) {
			// It's okay if the file doesn't exist yet
			return m, nil
		}
		return nil, err
	}

	return m, nil
}

// load reads the config file before any runtime can be started.
func (m *Registry) load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, err := os.Open(m.path)
	if err != nil {
		return err
	}
	defer f.Close()

	var list []TailnetConfig
	if err := json.NewDecoder(f).Decode(&list); err != nil {
		return err
	}

	m.entries = make(map[int]*tailnetEntry)
	m.nextID = 1

	for _, c := range list {
		c := c
		runtime := tailscale.NewRuntime(c.ID, c.StateDir, c.Hostname, c.SocksPort)
		m.entries[c.ID] = &tailnetEntry{config: c, runtime: runtime}

		// Update nextID based on loaded IDs
		if c.ID >= m.nextID {
			m.nextID = c.ID + 1
		}
	}

	return nil
}

func (m *Registry) saveConfigsLocked() error {
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	f, err := os.Create(m.path)
	if err != nil {
		return err
	}
	defer f.Close()

	list := make([]TailnetConfig, 0, len(m.entries))
	for _, entry := range m.entries {
		list = append(list, entry.config)
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

func (m *Registry) configuredTailnets() []*tailnetEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tailnets := make([]*tailnetEntry, 0, len(m.entries))
	for _, entry := range m.entries {
		tailnets = append(tailnets, entry)
	}

	sort.Slice(tailnets, func(i, j int) bool {
		return tailnets[i].config.ID < tailnets[j].config.ID
	})

	return tailnets
}

// List derives presentation data from persisted config and authoritative
// Tailscale status. Runtime status is never cached by Tailhopper.
func (m *Registry) List(ctx context.Context) []TailnetView {
	configured := m.configuredTailnets()

	tailnets := make([]TailnetView, 0, len(configured))
	for _, entry := range configured {
		info := entry.runtime.Info(ctx)
		view := m.view(entry, info)
		tailnets = append(tailnets, view)
	}
	return tailnets
}

func (m *Registry) view(entry *tailnetEntry, info tailscale.Info) TailnetView {
	m.mu.Lock()
	defer m.mu.Unlock()

	config := entry.config
	view := TailnetView{
		ID:                 config.ID,
		ConfiguredHostname: config.Hostname,
		MagicDNSSuffix:     config.ClaimedMagicDNSSuffix,
		SocksPort:          config.SocksPort,
		UserEnabled:        config.UserEnabled,
		Started:            info.Started,
		Error:              info.Error,
	}
	if info.Status == nil {
		return view
	}
	view.BackendState = info.Status.BackendState
	view.AuthURL = info.Status.AuthURL
	if info.Status.Self != nil {
		view.NodeHostname = info.Status.Self.HostName
	}
	if info.Status.CurrentTailnet != nil {
		discovered := info.Status.CurrentTailnet.MagicDNSSuffix
		if discovered != "" && config.ClaimedMagicDNSSuffix == "" {
			for otherID, other := range m.entries {
				if otherID != config.ID && other.config.ClaimedMagicDNSSuffix == discovered {
					view.Error = fmt.Sprintf("magic DNS suffix '%s' is already claimed by another tailnet", discovered)
					return view
				}
			}
			entry.config.ClaimedMagicDNSSuffix = discovered
			view.MagicDNSSuffix = discovered
			if err := m.saveConfigsLocked(); err != nil {
				view.Error = err.Error()
			}
		} else if discovered != "" && discovered != config.ClaimedMagicDNSSuffix {
			view.Error = fmt.Sprintf("tailnet reported unexpected magic DNS suffix '%s'", discovered)
		}
	}
	for _, peer := range info.Status.Peer {
		addresses := make([]netip.Prefix, 0, len(peer.TailscaleIPs))
		for _, address := range peer.TailscaleIPs {
			addresses = append(addresses, netip.PrefixFrom(address, address.BitLen()))
		}
		view.Peers = append(view.Peers, Peer{
			Name:      peer.HostName,
			DNSName:   strings.TrimSuffix(peer.DNSName, "."),
			Online:    peer.Online,
			Addresses: addresses,
		})
	}
	return view
}

// Start records enabled intent before starting runtime resources. A startup
// failure leaves the intent enabled so it can be restored on the next launch.
func (m *Registry) Start(ctx context.Context, id int) error {
	entry, err := m.entry(id)
	if err != nil {
		return err
	}
	entry.op.Lock()
	defer entry.op.Unlock()
	if err := m.setEnabled(entry, true); err != nil {
		return err
	}
	if err := entry.runtime.Start(ctx); err != nil {
		return err
	}
	// Discover persistent Tailnet identity immediately; later status is read by
	// dashboard and PAC requests.
	m.view(entry, entry.runtime.Info(ctx))
	return nil
}

// Stop records disabled intent before stopping runtime resources.
func (m *Registry) Stop(ctx context.Context, id int) error {
	entry, err := m.entry(id)
	if err != nil {
		return err
	}
	entry.op.Lock()
	defer entry.op.Unlock()
	if err := m.setEnabled(entry, false); err != nil {
		return err
	}
	return entry.runtime.Stop(ctx)
}

func (m *Registry) entry(id int) (*tailnetEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.entries[id]
	if !ok {
		return nil, ErrTailnetNotFound
	}
	return entry, nil
}

func (m *Registry) setEnabled(entry *tailnetEntry, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.entries[entry.config.ID] != entry {
		return ErrTailnetNotFound
	}

	previous := entry.config.UserEnabled
	if previous == enabled {
		return nil
	}
	entry.config.UserEnabled = enabled
	if err := m.saveConfigsLocked(); err != nil {
		entry.config.UserEnabled = previous
		return err
	}
	return nil
}

// RestoreEnabledTailnets starts tailnets that were user-enabled before shutdown.
func (m *Registry) RestoreEnabledTailnets(ctx context.Context) {
	configured := m.configuredTailnets()

	for _, entry := range configured {
		m.mu.RLock()
		enabled := m.entries[entry.config.ID] == entry && entry.config.UserEnabled
		m.mu.RUnlock()
		if !enabled {
			continue
		}

		info := entry.runtime.Info(ctx)
		if info.Started {
			continue
		}

		slog.InfoContext(ctx, "restoring enabled tailnet",
			slog.String("component", "registry"),
			slog.Int("tailnet_id", entry.config.ID),
		)

		if err := entry.runtime.Start(ctx); err != nil {
			slog.ErrorContext(ctx, "failed to restore enabled tailnet",
				slog.String("component", "registry"),
				slog.Int("tailnet_id", entry.config.ID),
				slog.Any("error", err),
			)
		}
	}
}

// Add creates a new unconfigured tailnet with the given hostname and returns its ID.
// If hostname is empty, a default one will be generated based on the machine's hostname.
// Example: if the machine's hostname is "laptop", the generated hostname will be "laptop-tailhopper".
func (m *Registry) Add(hostname string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, entry := range m.entries {
		if entry.config.ClaimedMagicDNSSuffix == "" {
			return 0, ErrUnconfiguredTailnet
		}
	}

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
		return 0, fmt.Errorf("failed to find available port: %w", err)
	}

	c := TailnetConfig{
		ID:        id,
		StateDir:  stateDir,
		Hostname:  hostname,
		SocksPort: socksPort,
	}

	runtime := tailscale.NewRuntime(c.ID, c.StateDir, c.Hostname, c.SocksPort)

	m.entries[c.ID] = &tailnetEntry{config: c, runtime: runtime}

	// Rollback on save failure
	if err := m.saveConfigsLocked(); err != nil {
		delete(m.entries, c.ID)
		return 0, err
	}

	return id, nil
}

// Delete removes a tailnet from the registry and deletes its state directory from disk.
func (m *Registry) Delete(id int) error {
	entry, err := m.entry(id)
	if err != nil {
		return err
	}
	entry.op.Lock()
	defer entry.op.Unlock()

	if err := entry.runtime.Stop(context.Background()); err != nil {
		slog.Warn("failed to stop tailnet during deletion",
			slog.String("component", "registry"),
			slog.Int("tailnet_id", id),
			slog.Any("error", err))
	}

	if entry.config.StateDir != "" {
		// Delete the state directory from disk
		if err := os.RemoveAll(entry.config.StateDir); err != nil {
			slog.Error("failed to remove state directory", slog.String("component", "registry"), slog.String("dir", entry.config.StateDir), slog.Any("error", err))
			// Continue with deletion even if directory removal fails
		}
	}

	m.mu.Lock()
	if m.entries[id] != entry {
		m.mu.Unlock()
		return ErrTailnetNotFound
	}
	delete(m.entries, id)

	if err := m.saveConfigsLocked(); err != nil {
		// The tailnet is gone from memory but the on-disk config still lists it;
		// it will be pruned on the next successful save. Best effort.
		slog.Error("failed to persist config after delete",
			slog.String("component", "registry"),
			slog.Int("tailnet_id", id),
			slog.Any("error", err))
	}
	m.mu.Unlock()

	return nil
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
