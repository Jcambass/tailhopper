package ts

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/jcambass/tailhopper/internal/logging"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

type watcher struct {
	state       IPNState
	mu          sync.RWMutex
	wg          *sync.WaitGroup
	cancel      context.CancelFunc
	logger      *slog.Logger
	localClient *local.Client
	onState     func(context.Context, IPNState)
}

// NewWatcher creates and starts a new IPN bus watcher.
// The caller must call Close() to clean up resources.
func NewWatcher(localClient *local.Client, onState func(context.Context, IPNState), tailnetID int) (*watcher, error) {
	w := &watcher{
		wg:          &sync.WaitGroup{},
		logger:      slog.Default().With(slog.String("component", "watcher"), slog.Int("tailnet_id", tailnetID)),
		localClient: localClient,
		onState:     onState,
	}
	if err := w.start(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *watcher) start() error {
	w.logger.Debug("Starting IPN watcher")
	if w.localClient == nil {
		return fmt.Errorf("local client is nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	w.mu.Lock()
	w.cancel = cancel
	w.mu.Unlock()

	w.wg.Go(func() {
		defer logging.CatchPanic(context.Background())
		defer w.logger.Debug("IPN watcher goroutine exiting")

		watcher, err := w.localClient.WatchIPNBus(ctx, ipn.NotifyInitialState|ipn.NotifyInitialStatus|ipn.NotifyPeerChanges)
		if err != nil {
			if ctx.Err() != nil {
				w.logger.Debug("IPN watcher canceled before subscription was established", slog.Any("error", err))
			} else {
				w.logger.Error("failed to watch IPN bus", slog.Any("error", err))
			}
			return
		}
		defer func() {
			if err := watcher.Close(); err != nil {
				w.logger.Warn("failed to close IPN watcher", slog.Any("error", err))
			}
		}()

		for {
			n, err := watcher.Next()
			if err != nil {
				if ctx.Err() != nil {
					w.logger.Debug("IPN watcher stopped", slog.Any("error", err))
				} else {
					w.logger.Warn("IPN watcher error", slog.Any("error", err))
				}

				return
			}

			w.logger.Debug("Received IPN notification", slog.String("notification", n.String()))
			var st *ipnstate.Status
			switch {
			case n.InitialStatus != nil:
				st = n.InitialStatus
			case needsStatusRefresh(&n):
				st, err = w.localClient.Status(ctx)
				if err != nil {
					w.logger.Warn("failed to fetch status", slog.Any("error", err))
					st = nil
				}
			}
			w.mu.Lock()
			w.state = w.state.refresh(&n, st)
			w.mu.Unlock()
			w.logger.Debug("Updated IPN state", slog.String("state", w.state.String()))
			w.onState(ctx, w.state)
		}
	})
	return nil
}

// Close stops the watcher and waits for the goroutine to exit.
func (w *watcher) Close() error {
	w.mu.Lock()
	cancel := w.cancel
	w.cancel = nil
	w.mu.Unlock()

	w.logger.Debug("Canceling IPN watcher context")
	if cancel != nil {
		cancel()
	}

	w.logger.Debug("Waiting for IPN watcher goroutine to exit")
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		w.logger.Debug("IPN watcher stopped successfully")
	case <-time.After(5 * time.Second):
		w.logger.Error("IPN watcher shutdown timeout - goroutine did not exit")
	}
	return nil
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

	//// From ipnstate.Status
	// SelfHostname is the current node's short name.
	SelfHostname string
	// MagicDNSSuffix is the MagicDNS suffix for the Tailnet, if any.
	// This can be used for routing but will not work for shared-in nodes or if magic DNS is disabled.
	MagicDNSSuffix string

	// Peers is the list of peers in the Tailnet.
	Peers []Peer
}

type Peer struct {
	// DNSName is the peer's MagicDNS FQDN, e.g. "host.tail-scale.ts.net.".
	DNSName      string
	ComputedName string
	Online       bool
	Addresses    []netip.Addr
}

// needsStatusRefresh reports whether n signals a change in self or peers.
// The netmap is no longer delivered over the IPN bus, so these are re-read via Status.
func needsStatusRefresh(n *ipn.Notify) bool {
	return n.SelfChange != nil || len(n.PeersChanged) > 0 || len(n.PeersRemoved) > 0 ||
		len(n.PeerChangedPatch) > 0 || (n.State != nil && *n.State == ipn.Running)
}

func (s IPNState) refresh(n *ipn.Notify, st *ipnstate.Status) IPNState {
	if n.State != nil {
		s.State = n.State
	}
	if n.ErrMessage != nil {
		s.ErrMessage = n.ErrMessage
	}
	if n.BrowseToURL != nil {
		s.BrowseToURL = n.BrowseToURL
	}
	if st == nil {
		return s
	}
	if st.Self != nil && st.Self.DNSName != "" {
		s.SelfHostname = computedName(st.Self)
		// TODO: Explicitly error out if magic DNS is disabled
		// Or find a sane way to handle these cases.
		// We might still get the proper value here but we can't connect to it.
		// Maybe handle somewhere else?
		if magicDNSSuffix := extractMagicDNSSuffix(st.Self.DNSName); magicDNSSuffix != "" {
			s.MagicDNSSuffix = magicDNSSuffix
		}
	}
	s.Peers = make([]Peer, 0, len(st.Peer))
	for _, ps := range st.Peer {
		s.Peers = append(s.Peers, Peer{
			DNSName:      ps.DNSName,
			ComputedName: computedName(ps),
			Online:       ps.Online,
			Addresses:    ps.TailscaleIPs,
		})
	}
	return s
}

func computedName(ps *ipnstate.PeerStatus) string {
	if name, _, _ := strings.Cut(strings.TrimSuffix(ps.DNSName, "."), "."); name != "" {
		return name
	}
	return ps.HostName
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

	if s.SelfHostname != "" {
		fmt.Fprintf(&sb, "selfNode=%v ", s.SelfHostname)
	}
	if len(s.Peers) > 0 {
		fmt.Fprintf(&sb, "peers=%d ", len(s.Peers))
	}
	str := sb.String()
	if str == "IPNState{" {
		return "IPNState{}"
	} else {
		return str[0:len(str)-1] + "}"
	}
}
