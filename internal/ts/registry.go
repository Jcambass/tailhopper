package ts

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/jcambass/tailhopper/internal/logging"
	"tailscale.com/util/dnsname"
)

type Config struct {
	// ID is a unique stable identifier for the tailnet.
	ID string `json:"id"`
	// StateDir is the path to the directory where the tailnet stores its state.
	StateDir string `json:"state_dir"`
	// SocksPort is the port on which the SOCKS5 proxy listens.
	SocksPort int `json:"socks_port"`
	// Hostname is the hostname used for the tailnet device.
	Hostname string `json:"hostname"`
	// LockedDomain is the domain we expect this tailnet to be logged into.
	// If empty, it will be set upon first successful connection.
	LockedDomain string `json:"locked_domain,omitempty"`
	// TerminalError stores a fatal error that prevents the tailnet from starting.
	TerminalError string `json:"terminal_error,omitempty"`
}

type Registry struct {
	path   string
	mu     sync.RWMutex
	logger *logging.Logger
	nextID int

	// configs maps ID to Config
	configs map[string]Config
	// tailnets maps ID to *Tailnet
	tailnets map[string]*Tailnet
	// stateChangeNotifier is called when any tailnet state changes
	stateChangeNotifier func(tailnetID string)
}

func NewRegistry(path string, logger *logging.Logger) (*Registry, error) {
	logger = logger.With("component", "registry")

	m := &Registry{
		path:     path,
		logger:   logger,
		nextID:   1,
		configs:  make(map[string]Config),
		tailnets: make(map[string]*Tailnet),
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

func (m *Registry) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, err := os.Open(m.path)
	if err != nil {
		return err
	}
	defer f.Close()

	var list []Config
	if err := json.NewDecoder(f).Decode(&list); err != nil {
		return err
	}

	m.configs = make(map[string]Config)
	m.tailnets = make(map[string]*Tailnet)
	m.nextID = 1
	for _, c := range list {
		m.configs[c.ID] = c
		// Create and initialize Tailnet instance from config
		domainLocker := func(domain string) error {
			return m.SetLockedDomain(c.ID, domain)
		}
		terminalErrorSaver := m.createTerminalErrorSaver(c.ID)
		stateNotifier := m.createStateNotifier(c.ID)
		tailnet := NewTailnet(c.ID, c.StateDir, c.Hostname, c.LockedDomain, c.TerminalError, c.SocksPort, m.logger.With("tailnet", c.ID), domainLocker, terminalErrorSaver, stateNotifier)
		m.tailnets[c.ID] = tailnet
		// Update nextID based on loaded IDs
		if id, err := strconv.Atoi(c.ID); err == nil && id >= m.nextID {
			m.nextID = id + 1
		}
	}

	return nil
}

func (m *Registry) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked()
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

	list := make([]Config, 0, len(m.configs))
	for _, c := range m.configs {
		list = append(list, c)
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

func (m *Registry) List() []*Tailnet {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Collect IDs and sort them numerically
	ids := make([]string, 0, len(m.tailnets))
	for id := range m.tailnets {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		iNum, _ := strconv.Atoi(ids[i])
		jNum, _ := strconv.Atoi(ids[j])
		return iNum < jNum
	})

	// Return tailnets in sorted order
	list := make([]*Tailnet, 0, len(ids))
	for _, id := range ids {
		if t, ok := m.tailnets[id]; ok {
			list = append(list, t)
		}
	}
	return list
}

func (m *Registry) Add(hostname string) (*Tailnet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := fmt.Sprintf("%d", m.nextID)
	m.nextID++
	stateDir := filepath.Join(filepath.Dir(m.path), "tailnets", id)

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

	c := Config{
		ID:        id,
		StateDir:  stateDir,
		Hostname:  hostname,
		SocksPort: socksPort,
	}

	m.configs[c.ID] = c

	// Create and initialize Tailnet instance
	domainLocker := func(domain string) error {
		return m.SetLockedDomain(c.ID, domain)
	}
	terminalErrorSaver := m.createTerminalErrorSaver(c.ID)
	stateNotifier := m.createStateNotifier(c.ID)
	tailnet := NewTailnet(c.ID, c.StateDir, c.Hostname, "", "", c.SocksPort, m.logger.With("tailnet", c.ID), domainLocker, terminalErrorSaver, stateNotifier)
	m.tailnets[c.ID] = tailnet

	if err := m.saveLocked(); err != nil {
		// Rollback on save failure
		delete(m.configs, c.ID)
		delete(m.tailnets, c.ID)
		return nil, err
	}

	// Notify about global change (new tailnet added)
	if m.stateChangeNotifier != nil {
		m.stateChangeNotifier("")
	}

	return tailnet, nil
}

func (m *Registry) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.configs[id]; !exists {
		return fmt.Errorf("tailnet not found")
	}

	delete(m.configs, id)
	delete(m.tailnets, id)

	err := m.saveLocked()
	if err == nil && m.stateChangeNotifier != nil {
		// Notify about global change (tailnet deleted)
		m.stateChangeNotifier("")
	}

	return err
}

func (m *Registry) Get(id string) (*Tailnet, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tailnets[id]
	return t, ok
}

// TODO: Still allows adding two tailnets with same domain!
// SetLockedDomain updates the LockedDomain for a tailnet.
// It checks if the domain is already claimed by another tailnet to prevent duplicates.
func (m *Registry) SetLockedDomain(id string, domain string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	config, ok := m.configs[id]
	if !ok {
		return fmt.Errorf("tailnet not found")
	}

	// If the domain is already set to the same value, do nothing
	if config.LockedDomain == domain {
		return nil
	}

	// If the config already has a different locked domain, this is a mismatch!
	if config.LockedDomain != "" && config.LockedDomain != domain {
		return fmt.Errorf("tailnet %s is locked to domain %s, but attempted to use %s", id, config.LockedDomain, domain)
	}

	// Check for duplicates across other tailnets
	for _, other := range m.configs {
		if other.ID != id && other.LockedDomain == domain {
			return fmt.Errorf("domain %s is already used by tailnet %s", domain, other.ID)
		}
	}

	config.LockedDomain = domain
	m.configs[id] = config
	
	// Update the tailnet's locked domain as well
	if tailnet, ok := m.tailnets[id]; ok {
		tailnet.setLockedDomain(domain)
	}
	
	return m.saveLocked()
}

// HasUnconfiguredTailnets returns true if any tailnet hasn't been configured (no LockedDomain set).
func (m *Registry) HasUnconfiguredTailnets() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, config := range m.configs {
		if config.LockedDomain == "" {
			return true
		}
	}
	return false
}

// SetStateChangeNotifier sets the callback for state changes.
func (m *Registry) SetStateChangeNotifier(notifier func(tailnetID string)) {
	m.stateChangeNotifier = notifier
}

// createStateNotifier creates a state notifier function for a specific tailnet.
func (m *Registry) createStateNotifier(tailnetID string) func() {
	return func() {
		if m.stateChangeNotifier != nil {
			m.stateChangeNotifier(tailnetID)
		}
	}
}

// createTerminalErrorSaver creates a function to save terminal errors for a specific tailnet.
func (m *Registry) createTerminalErrorSaver(tailnetID string) func(error string) error {
	return func(errMsg string) error {
		return m.SetTerminalError(tailnetID, errMsg)
	}
}

// SetTerminalError updates the TerminalError for a tailnet and persists it.
func (m *Registry) SetTerminalError(id string, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	config, ok := m.configs[id]
	if !ok {
		return fmt.Errorf("tailnet not found")
	}

	config.TerminalError = errMsg
	m.configs[id] = config

	return m.saveLocked()
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
