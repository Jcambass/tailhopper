package ts

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/jcambass/tailhopper/internal/logging"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
)

type watcher struct {
	tailnet       *Tailnet
	ipnBusWatcher *local.IPNBusWatcher
	state         IPNState
	wg            *sync.WaitGroup
	cancel        context.CancelFunc
	logger        *slog.Logger
}

func newWatcher(tailnet *Tailnet) *watcher {
	return &watcher{
		tailnet: tailnet,
		wg:      &sync.WaitGroup{},
		logger:  slog.Default().With("component", "watcher", "tailnet_id", tailnet.id),
	}
}

func (w *watcher) Start() {
	w.logger.Info("Starting IPN watcher")
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel

	w.wg.Go(func() {
		defer logging.CatchPanic(context.Background())
		defer w.logger.Info("IPN watcher goroutine exiting")

		// TODO: Do we need something like this?
		// Wait a moment for tsnet to initialize
		//time.Sleep(500 * time.Millisecond)

		lc, err := w.tailnet.server.LocalClient()
		if err != nil {
			w.logger.Info("failed to get LocalClient for watcher", "error", err)
			return
		}

		// TODO: Use NotifyWatchEngineUpdates?
		// TODO: Use NotifyInitialHealthState?
		watcher, err := lc.WatchIPNBus(ctx, ipn.NotifyInitialState|ipn.NotifyInitialNetMap)
		if err != nil {
			w.logger.Info("failed to watch IPN bus", "error", err)
			return
		}
		w.ipnBusWatcher = watcher
		defer watcher.Close()

		for {
			n, err := watcher.Next()
			if err != nil {
				w.logger.Info("IPN watcher error", "error", err)
				// The watcher can close due to tailnet shutdown; ignore and exit.
				// Ideally we could distinguish between expected closure and unexpected errors.

				return
			}

			w.logger.Info("Received IPN notification", "notification", n.String())
			w.state = w.state.refresh(&n)
			w.logger.Info("Updated IPN state", "state", w.state.String())
			w.tailnet.ReactToIPNStateChange(ctx, w.state)
			w.logger.Info("Tailnet after reacting to IPN state change", "tailnet", w.tailnet.String())
		}
	})
}

// TODO: Probably should return an error if Close() fails?
func (w *watcher) Stop() {
	if w.ipnBusWatcher != nil {
		// TODO: Are all these needed?
		w.logger.Info("Canceling IPN watcher ctx")
		w.cancel()
		w.cancel = nil
		w.logger.Info("Closing IPN watcher")
		w.ipnBusWatcher.Close()
		w.ipnBusWatcher = nil
		w.logger.Info("IPN watcher closed, waiting for goroutine to exit")
		w.wg.Wait()
		w.logger.Info("IPN watcher stopped successfully")
	}
}

type IPNState struct {
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

func (s IPNState) refresh(n *ipn.Notify) IPNState {
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
			// We might still get the proper value here but we can't connect to it.
			// Maybe handle somewhere else?
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

func (s IPNState) String() string {
	var sb strings.Builder
	sb.WriteString("IPNState{")
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
	if str == "IPNState{" {
		return "IPNState{}"
	} else {
		return str[0:len(str)-1] + "}"
	}
}
