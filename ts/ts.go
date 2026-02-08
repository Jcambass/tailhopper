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

// Server wraps a tsnet.Server with state management.
type Server struct {
	*tsnet.Server
	state        *StateMachine
	stateDir     string
	cachedDomain string
	mu           sync.RWMutex
}

// NewServer creates a new Tailscale server.
func NewServer(stateDir, hostname string) *Server {
	s := &Server{
		Server: &tsnet.Server{
			Dir:      stateDir,
			Hostname: hostname,
		},
		state:    NewStateMachine(),
		stateDir: stateDir,
	}
	// Load cached domain from disk
	s.loadCachedDomain()
	return s
}

// State returns the state machine for observing connection state.
func (s *Server) State() *StateMachine {
	return s.state
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

// startIPNWatcher watches for IPN state changes and updates the state machine.
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
				s.state.SetError(err)
				return
			}
			if n.State != nil {
				state := *n.State
				log.Printf("IPN state changed: %v", state)
				switch state {
				case ipn.Running:
					log.Printf("tsnet fully connected")
					s.updateBaseDomain()
					s.state.SetRunning()
				case ipn.NeedsLogin:
					log.Printf("tsnet needs login")
					s.clearCachedDomain()
					// Get auth URL from status
					if status, err := lc.Status(ctx); err == nil && status.AuthURL != "" {
						log.Printf("Auth URL: %s", status.AuthURL)
						s.state.SetNeedsLogin(status.AuthURL)
					}
				case ipn.NeedsMachineAuth:
					log.Printf("tsnet needs machine auth (admin approval)")
					s.state.SetNeedsMachineAuth()
				case ipn.Stopped:
					log.Printf("tsnet stopped/disconnected")
					s.state.SetConnecting()
				case ipn.Starting:
					log.Printf("tsnet starting")
					s.state.SetConnecting()
				}
			}
			// Also check for auth URL in BrowseToURL notifications
			if n.BrowseToURL != nil && *n.BrowseToURL != "" {
				log.Printf("Auth URL from notification: %s", *n.BrowseToURL)
				s.state.SetNeedsLogin(*n.BrowseToURL)
			}
		}
	}()
}

// Start begins the tsnet connection in the background.
func (s *Server) Start() {
	s.startIPNWatcher()

	go func() {
		if _, err := s.Up(context.Background()); err != nil {
			log.Printf("tsnet error: %v", err)
			s.state.SetError(err)
			return
		}
		// IPN watcher will set Running state when tsnet is fully connected
	}()
}

// BaseDomain returns the Tailnet base domain (e.g., "tail1234.ts.net").
func (s *Server) BaseDomain() string {
	s.mu.RLock()
	cached := s.cachedDomain
	s.mu.RUnlock()
	if cached != "" {
		return cached
	}

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
