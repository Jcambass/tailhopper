package ts

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/jcambass/tailhopper/internal/logging"
	"github.com/jcambass/tailhopper/internal/socks"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
)

// LifecycleState represents the lifecycle state of a Tailnet.
type LifecycleState string

const (
	// LifecycleStopped indicates the Tailnet is fully stopped.
	LifecycleStopped LifecycleState = "stopped"
	// LifecycleStopping indicates the Tailnet is shutting down.
	LifecycleStopping LifecycleState = "stopping"
	// LifecycleStarted indicates the Tailnet is fully started and running.
	LifecycleStarted LifecycleState = "started"
	// LifecycleStarting indicates the Tailnet is starting up.
	LifecycleStarting LifecycleState = "starting"
)

type stateView struct {
	//// From Notify.*
	// State is the current state of the Tailscale connection.
	State *ipn.State
	// ErrMessage, if non-nil, contains a critical error message.
	// For State InUseOtherUser, ErrMessage is not critical and just contains the details.
	ErrMessage *string
	// BrowseToURL, if non-nil, UI should open a browser right now
	BrowseToURL *string

	//// From Notify.NetMap.*
	// SelfNode is the current node's view of itself.
	SelfNode tailcfg.NodeView
	// MagicDNSSuffix is the MagicDNS suffix for the Tailnet, if any.
	// This can be used for routing but will not work for shared-in nodes or if magic DNS is disabled.
	MagicDNSSuffix string

	// Peers is the list of peers in the Tailnet.
	Peers []tailcfg.NodeView
	// DisplayMessages are the list of health check problems for this
	// node from the perspective of the control plane.
	// If empty, there are no known problems from the control plane's
	// point of view, but the node might know about its own health
	// check problems.
	DisplayMessages map[tailcfg.DisplayMessageID]tailcfg.DisplayMessage
}

func (s stateView) mergeWithNotify(n *ipn.Notify) stateView {
	if n.State != nil {
		s.State = n.State
	}
	if n.ErrMessage != nil {
		s.ErrMessage = n.ErrMessage
	}
	if n.BrowseToURL != nil {
		s.BrowseToURL = n.BrowseToURL
	}
	if n.NetMap != nil {
		s.SelfNode = n.NetMap.SelfNode
		if n.NetMap.SelfNode.Valid() && n.NetMap.SelfNode.Name() != "" {
			// TODO: Explicitly error out if magic DNS is disabled
			// Or find a sane way to handle these cases.
			magicDNSSuffix := extractMagicDNSSuffix(n.NetMap.SelfNode.Name())
			if magicDNSSuffix != "" {
				s.MagicDNSSuffix = magicDNSSuffix
			}
		}
		s.Peers = n.NetMap.Peers
		s.DisplayMessages = n.NetMap.DisplayMessages
	}
	return s
}

// Example: "host.tail-scale.ts.net." -> "tail-scale.ts.net"
// Just removed any trailing dot and the first label.
func extractMagicDNSSuffix(fqdn string) string {
	fqdn = strings.TrimSuffix(fqdn, ".")
	parts := strings.SplitN(fqdn, ".", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

func (s stateView) String() string {
	var sb strings.Builder
	sb.WriteString("stateView{")
	if s.State != nil {
		fmt.Fprintf(&sb, "state=%v ", *s.State)
	}
	if s.ErrMessage != nil {
		fmt.Fprintf(&sb, "err=%q ", *s.ErrMessage)
	}
	if s.BrowseToURL != nil {
		fmt.Fprintf(&sb, "url=%q ", *s.BrowseToURL)
	}

	if s.SelfNode.Valid() {
		fmt.Fprintf(&sb, "selfNode=%v ", s.SelfNode.ComputedName())
	}
	if len(s.Peers) > 0 {
		fmt.Fprintf(&sb, "peers=%d ", len(s.Peers))
	}
	if len(s.DisplayMessages) > 0 {
		fmt.Fprintf(&sb, "displayMessages=%d ", len(s.DisplayMessages))
	}
	str := sb.String()
	if str == "stateView{" {
		return "stateView{}"
	} else {
		return str[0:len(str)-1] + "}"
	}
}

type Tailnet struct {
	id              string
	tsnetStateDir   string
	userSetHostname string
	socksPort       int
	lockedDomain    string

	latestState    stateView
	lifecycleMu    sync.RWMutex
	lifecycleState LifecycleState

	logger *logging.Logger

	server     *tsnet.Server
	watcher    *watcher
	socksProxy *socks.Server

	lifecycleOpMu *sync.RWMutex

	// terminalError stores a fatal error that prevents the tailnet from starting
	terminalErrorMu sync.RWMutex
	terminalError   string

	// domainLocker is called when a domain is detected for this tailnet
	domainLocker func(domain string) error
	// terminalErrorSaver is called when a terminal error is set to persist it
	terminalErrorSaver func(errMsg string) error
	// stateNotifier is called when the state of this tailnet changes
	stateNotifier func()
}

func NewTailnet(id string, tsnetStateDir string, hostname string, lockedDomain string, terminalError string, socksPort int, logger *logging.Logger, domainLocker func(domain string) error, terminalErrorSaver func(errMsg string) error, stateNotifier func()) *Tailnet {
	if logger == nil {
		logger = logging.Default().With("component", "tailnet")
	}

	return &Tailnet{
		id:                 id,
		tsnetStateDir:      tsnetStateDir,
		userSetHostname:    hostname,
		socksPort:          socksPort,
		lockedDomain:       lockedDomain,
		terminalError:      terminalError,
		logger:             logger,
		lifecycleOpMu:      &sync.RWMutex{},
		lifecycleState:     LifecycleStopped,
		domainLocker:       domainLocker,
		terminalErrorSaver: terminalErrorSaver,
		stateNotifier:      stateNotifier,
		latestState: stateView{
			MagicDNSSuffix: lockedDomain,
		},
	}
}

func (t *Tailnet) ID() string {
	return t.id
}

func (t *Tailnet) LockedDomain() string {
	return t.lockedDomain
}

func (t *Tailnet) setLockedDomain(domain string) {
	t.lockedDomain = domain
}

func (t *Tailnet) LatestState() stateView {
	return t.latestState
}

func (t *Tailnet) LifecycleState() LifecycleState {
	t.lifecycleMu.RLock()
	defer t.lifecycleMu.RUnlock()
	return t.lifecycleState
}

func (t *Tailnet) TerminalError() string {
	t.terminalErrorMu.RLock()
	defer t.terminalErrorMu.RUnlock()
	return t.terminalError
}

func (t *Tailnet) setTerminalError(err string) {
	t.terminalErrorMu.Lock()
	t.terminalError = err
	t.terminalErrorMu.Unlock()

	// Persist the terminal error
	if t.terminalErrorSaver != nil {
		if saveErr := t.terminalErrorSaver(err); saveErr != nil {
			t.logger.Printf("failed to save terminal error: %v", saveErr)
		}
	}

	if t.stateNotifier != nil {
		t.stateNotifier()
	}
}

func (t *Tailnet) setLifecycleState(state LifecycleState) {
	t.lifecycleMu.Lock()
	if t.lifecycleState == state {
		t.lifecycleMu.Unlock()
		return
	}
	t.lifecycleState = state
	t.lifecycleMu.Unlock()

	if t.stateNotifier != nil {
		t.stateNotifier()
	}
}

func (t *Tailnet) UpdateLatestState(n *ipn.Notify) {
	oldSuffix := t.latestState.MagicDNSSuffix
	t.latestState = t.latestState.mergeWithNotify(n)

	// If we just learned the domain, lock it
	if oldSuffix == "" && t.latestState.MagicDNSSuffix != "" && t.domainLocker != nil {
		if err := t.domainLocker(t.latestState.MagicDNSSuffix); err != nil {
			t.logger.Printf("failed to lock domain %s: %v", t.latestState.MagicDNSSuffix, err)
			// This is a terminal error - the tailnet is trying to use a domain that's already in use
			t.setTerminalError(err.Error())
			// Stop the tailnet since it cannot proceed
			go func() {
				if err := t.Stop(context.Background()); err != nil {
					t.logger.Printf("failed to stop tailnet after domain lock failure: %v", err)
				}
			}()
			return
		}
	}

	// Notify about the state change
	if t.stateNotifier != nil {
		t.stateNotifier()
	}
}

func (t *Tailnet) Start(ctx context.Context) error {
	// Check for terminal errors first
	if termErr := t.TerminalError(); termErr != "" {
		return fmt.Errorf("tailnet has a terminal error and cannot be started: %s", termErr)
	}

	if !t.lifecycleOpMu.TryLock() {
		return errors.New("tailnet is in the process of starting or stopping")
	}
	if t.LifecycleState() == LifecycleStarting || t.LifecycleState() == LifecycleStarted {
		t.lifecycleOpMu.Unlock()
		return errors.New("tailnet that is already started cannot be started again")
	}
	t.setLifecycleState(LifecycleStarting)
	defer t.lifecycleOpMu.Unlock()
	defer t.setLifecycleState(LifecycleStarted)

	t.logger.Printf("Starting tailnet")

	// Reset previous state
	t.latestState = stateView{
		MagicDNSSuffix: t.lockedDomain, // Preserve locked domain
	}

	t.server = &tsnet.Server{
		Dir:      t.tsnetStateDir,
		Hostname: t.userSetHostname,
	}

	socksProxy, err := socks.NewServer(t.Dial, t.socksPort)
	if err != nil {
		t.logger.Printf("failed to start SOCKS5 proxy: %v", err)
		// At this point we haven't started any long-running processes, so we can just return the error without worrying about cleanup.
		return err
	}
	t.socksProxy = socksProxy
	t.socksProxy.Start()

	// start IPN watcher to observe state changes
	t.watcher = newWatcher(t)
	t.watcher.Start()

	// Asynchronously start the server
	err = t.server.Start()
	if err != nil {
		t.logger.Printf("failed to start tsnet server: %v", err)
		// If we fail to start the server, we should stop the watcher and socks proxy that we started since they won't be functional without the server.
		t.watcher.Stop()
		t.watcher = nil
		err := t.socksProxy.Close()
		if err != nil {
			t.logger.Printf("failed to close SOCKS5 proxy after server start failure: %v", err)
		}
		t.socksProxy = nil
		return err
	}

	return nil
}

func (t *Tailnet) Stop(ctx context.Context) error {
	if !t.lifecycleOpMu.TryLock() {
		return errors.New("tailnet is in the process of starting or stopping")
	}
	if t.LifecycleState() == LifecycleStopping || t.LifecycleState() == LifecycleStopped {
		t.lifecycleOpMu.Unlock()
		return errors.New("tailnet that is already stopped cannot be stopped again")
	}
	t.setLifecycleState(LifecycleStopping)
	defer t.lifecycleOpMu.Unlock()
	defer t.setLifecycleState(LifecycleStopped)

	t.logger.Printf("Stopping tailnet")

	if t.socksProxy != nil {
		t.logger.Printf("Stopping SOCKS5 proxy")
		err := t.socksProxy.Close()
		if err != nil {
			t.logger.Printf("failed to close SOCKS5 proxy: %v", err)
			return err
		}
		t.socksProxy = nil
		t.logger.Printf("SOCKS5 proxy stopped")
	}

	if t.watcher != nil {
		t.logger.Printf("Stopping watcher")
		t.watcher.Stop()
		t.watcher = nil
		t.logger.Printf("Watcher stopped")
	}

	if t.server != nil {
		t.logger.Printf("Stopping tsnet server")
		err := t.server.Close()
		if err != nil {
			t.logger.Printf("failed to close tsnet server: %v", err)
			return err
		}
		t.server = nil
		t.logger.Printf("tsnet server stopped")
	}

	t.logger.Printf("Tailnet stopped successfully")

	return nil
}

func (t *Tailnet) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	// TODO: Do we still need this lifecycle lock for Dial?
	// Depends on the rest of tsnets and our error handling.
	if !t.lifecycleOpMu.TryRLock() {
		return nil, errors.New("tailnet is in the process of starting or stopping")
	}
	defer t.lifecycleOpMu.RUnlock()

	return t.server.Dial(ctx, network, address)
}

func (t *Tailnet) SocksAddr() string {
	return fmt.Sprintf("localhost:%d", t.socksPort)
}
