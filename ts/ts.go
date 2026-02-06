// Package ts provides Tailscale network initialization and state management.
package ts

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

const baseDomainFile = "tailhopper-domain"

// StateChannels holds channels for communicating tsnet connection state.
type StateChannels struct {
	// ErrorCh receives fatal errors from tsnet initialization
	ErrorCh chan error
	// ReadyCh is closed when tsnet is fully connected
	ReadyCh chan struct{}
	// SlowCh is closed when connection is taking too long
	SlowCh chan struct{}
	// AuthURLCh receives auth URLs when login is needed
	AuthURLCh chan string
}

// NewStateChannels creates a new set of state channels.
func NewStateChannels() *StateChannels {
	return &StateChannels{
		ErrorCh:   make(chan error, 1),
		ReadyCh:   make(chan struct{}),
		SlowCh:    make(chan struct{}),
		AuthURLCh: make(chan string, 1),
	}
}

// Server wraps a tsnet.Server with state management.
type Server struct {
	*tsnet.Server
	channels     *StateChannels
	stateDir     string
	cachedDomain string
	mu           sync.RWMutex
}

// NewServer creates a new Tailscale server.
func NewServer(stateDir, hostname string, channels *StateChannels) *Server {
	s := &Server{
		Server: &tsnet.Server{
			Dir:      stateDir,
			Hostname: hostname,
		},
		channels: channels,
		stateDir: stateDir,
	}
	// Load cached domain from disk
	s.loadCachedDomain()
	return s
}

// loadCachedDomain reads the cached base domain from disk.
func (s *Server) loadCachedDomain() {
	path := filepath.Join(s.stateDir, baseDomainFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return // File doesn't exist yet, that's fine
	}
	s.mu.Lock()
	s.cachedDomain = strings.TrimSpace(string(data))
	s.mu.Unlock()
	log.Printf("Loaded cached domain: %s", s.cachedDomain)
}

// saveCachedDomain writes the base domain to disk.
func (s *Server) saveCachedDomain(domain string) {
	s.mu.Lock()
	s.cachedDomain = domain
	s.mu.Unlock()

	path := filepath.Join(s.stateDir, baseDomainFile)
	if err := os.WriteFile(path, []byte(domain), 0600); err != nil {
		log.Printf("Failed to save cached domain: %v", err)
	}
}

// clearCachedDomain removes the cached domain from memory and disk.
func (s *Server) clearCachedDomain() {
	s.mu.Lock()
	s.cachedDomain = ""
	s.mu.Unlock()

	path := filepath.Join(s.stateDir, baseDomainFile)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("Failed to remove cached domain file: %v", err)
	}
	log.Printf("Cleared cached domain (re-authentication required)")
}

// SlowTimeout is the duration after which the "still connecting" message is shown.
const SlowTimeout = 10 * time.Second

// startSlowTimeout closes SlowCh after the timeout if not ready.
func (s *Server) startSlowTimeout() {
	go func() {
		select {
		case <-time.After(SlowTimeout):
			close(s.channels.SlowCh)
		case <-s.channels.ReadyCh:
			// Connected before timeout, no need to show slow message
		}
	}()
}

// startIPNWatcher watches for IPN state changes and updates channels accordingly.
func (s *Server) startIPNWatcher() {
	go func() {
		// Wait a moment for tsnet to initialize
		time.Sleep(500 * time.Millisecond)

		lc, err := s.LocalClient()
		if err != nil {
			log.Printf("failed to get LocalClient for watcher: %v", err)
			return
		}

		ctx := context.Background()
		watcher, err := lc.WatchIPNBus(ctx, ipn.NotifyInitialState|ipn.NotifyNoPrivateKeys)
		if err != nil {
			log.Printf("failed to watch IPN bus: %v", err)
			return
		}
		defer watcher.Close()

		for {
			n, err := watcher.Next()
			if err != nil {
				log.Printf("IPN watcher error: %v", err)
				return
			}
			if n.State != nil {
				state := *n.State
				log.Printf("IPN state changed: %v", state)
				if state == ipn.Running {
					log.Printf("tsnet fully connected (AuthLoop running)")
					// Update cached domain now that we're connected
					s.updateBaseDomain()
					// Clear auth URL if we were showing login page
					select {
					case <-s.channels.AuthURLCh:
					default:
					}
					close(s.channels.ReadyCh)
					return
				}
				if state == ipn.NeedsLogin {
					log.Printf("tsnet needs login")
					// Clear cached domain since we may be switching tailnets
					s.clearCachedDomain()
					// Get the auth URL from the status
					status, err := lc.Status(ctx)
					if err == nil && status.AuthURL != "" {
						log.Printf("Auth URL: %s", status.AuthURL)
						// Clear old auth URL if any
						select {
						case <-s.channels.AuthURLCh:
						default:
						}
						s.channels.AuthURLCh <- status.AuthURL
					}
				}
			}
			// Also check for auth URL in BrowseToURL notifications
			if n.BrowseToURL != nil && *n.BrowseToURL != "" {
				log.Printf("Auth URL from notification: %s", *n.BrowseToURL)
				// Clear old auth URL if any
				select {
				case <-s.channels.AuthURLCh:
				default:
				}
				s.channels.AuthURLCh <- *n.BrowseToURL
			}
		}
	}()
}

// Start begins the tsnet connection in the background.
// It also starts the slow timeout and IPN watcher.
func (s *Server) Start() {
	s.startSlowTimeout()
	s.startIPNWatcher()

	go func() {
		// Use background context - tsnet will keep retrying and recover on its own
		if _, err := s.Up(context.Background()); err != nil {
			log.Printf("tsnet error: %v", err)
			s.channels.ErrorCh <- err
			return
		}
		// IPN watcher will close ReadyCh when state reaches Running
	}()
}

// BaseDomain returns the Tailnet base domain (e.g., "tail1234.ts.net").
// Returns cached value if available, otherwise queries the status.
func (s *Server) BaseDomain() string {
	// First check cache
	s.mu.RLock()
	cached := s.cachedDomain
	s.mu.RUnlock()
	if cached != "" {
		return cached
	}

	// Try to get from status
	domain := s.fetchBaseDomain()
	if domain != "" {
		s.saveCachedDomain(domain)
	}
	return domain
}

// fetchBaseDomain queries the Tailscale status for the base domain.
func (s *Server) fetchBaseDomain() string {
	lc, err := s.LocalClient()
	if err != nil {
		return ""
	}

	status, err := lc.Status(context.Background())
	if err != nil {
		return ""
	}

	// Try CurrentTailnet.MagicDNSSuffix (available when connected)
	if status.CurrentTailnet != nil && status.CurrentTailnet.MagicDNSSuffix != "" {
		return strings.TrimSuffix(status.CurrentTailnet.MagicDNSSuffix, ".")
	}

	return ""
}

// updateBaseDomain fetches and caches the base domain if available.
func (s *Server) updateBaseDomain() {
	if domain := s.fetchBaseDomain(); domain != "" {
		s.saveCachedDomain(domain)
		log.Printf("Updated cached domain: %s", domain)
	}
}
