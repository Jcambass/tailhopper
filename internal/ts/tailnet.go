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

func (s stateView) MergeWithNotify(n *ipn.Notify) stateView {
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
	tsnetStateDir   string
	userSetHostname string

	latestState stateView

	logger *logging.Logger

	server     *tsnet.Server
	watcher    *watcher
	socksProxy *socks.Server

	lifecycleMu *sync.RWMutex
}

func NewTailnet(tsnetStateDir string, hostname string, logger *logging.Logger) *Tailnet {
	if logger == nil {
		logger = logging.Default().With("component", "tailnet")
	}

	return &Tailnet{
		tsnetStateDir:   tsnetStateDir,
		userSetHostname: hostname,
		logger:          logger,
		lifecycleMu:     &sync.RWMutex{},
	}
}

func (t *Tailnet) LatestState() stateView {
	return t.latestState
}

func (t *Tailnet) UpdateLatestState(n *ipn.Notify) {
	t.latestState = t.latestState.MergeWithNotify(n)
}

func (t *Tailnet) Start(ctx context.Context) error {
	if !t.lifecycleMu.TryLock() {
		return errors.New("tailnet is in the process of starting or stopping")
	}
	defer t.lifecycleMu.Unlock()

	if t.latestState.State != nil && *t.latestState.State != ipn.Stopped && *t.latestState.State != ipn.NoState {
		return errors.New("tailnet that is already started cannot be started again")
	}

	t.logger.Printf("Starting tailnet")

	t.server = &tsnet.Server{
		Dir:      t.tsnetStateDir,
		Hostname: t.userSetHostname,
	}

	socksProxy, err := socks.NewServer(t.Dial)
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

	// Force the starting state into our world view
	starting := new(ipn.State)
	*starting = ipn.Starting
	t.UpdateLatestState(&ipn.Notify{
		State: starting,
	})

	// All other state is kept on purpose!

	return nil
}

func (t *Tailnet) Stop(ctx context.Context) error {
	if !t.lifecycleMu.TryLock() {
		return errors.New("tailnet is in the process of starting or stopping")
	}
	defer t.lifecycleMu.Unlock()

	if t.latestState.State == nil || *t.latestState.State == ipn.Stopped {
		return errors.New("tailnet that is not started cannot be stopped")
	}

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

	// Force the stopped state into our world view
	// as soon as the watcher has been stopped
	// otherwise it might overwrite our state again.
	stopped := new(ipn.State)
	*stopped = ipn.Stopped
	t.UpdateLatestState(&ipn.Notify{
		State: stopped,
	})

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
	if !t.lifecycleMu.TryRLock() {
		return nil, errors.New("tailnet is in the process of starting or stopping")
	}
	defer t.lifecycleMu.RUnlock()

	return t.server.Dial(ctx, network, address)
}

func (t *Tailnet) SocksAddr() (string, bool) {
	// TODO: Do we still need this lifecycle lock for SocksAddr?
	// Depends on the rest of tsnets and our error handling.
	if !t.lifecycleMu.TryRLock() {
		return "", false
	}
	defer t.lifecycleMu.RUnlock()

	if t.socksProxy == nil {
		panic("socks proxy is not initialized")
	}

	return t.socksProxy.Addr(), true
}
