package ts

import (
	"encoding/json"
	"fmt"
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
		tailnet := NewTailnet(c.ID, c.StateDir, c.Hostname, c.LockedDomain, m.logger.With("tailnet", c.ID), domainLocker)
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

	c := Config{
		ID:       id,
		StateDir: stateDir,
		Hostname: hostname,
	}

	m.configs[c.ID] = c

	// Create and initialize Tailnet instance
	domainLocker := func(domain string) error {
		return m.SetLockedDomain(c.ID, domain)
	}
	tailnet := NewTailnet(c.ID, c.StateDir, c.Hostname, "", m.logger.With("tailnet", c.ID), domainLocker)
	m.tailnets[c.ID] = tailnet

	if err := m.saveLocked(); err != nil {
		// Rollback on save failure
		delete(m.configs, c.ID)
		delete(m.tailnets, c.ID)
		return nil, err
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
	return m.saveLocked()
}

func (m *Registry) Get(id string) (*Tailnet, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tailnets[id]
	return t, ok
}

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
