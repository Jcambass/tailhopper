package ts

import (
	"context"
	"log"

	"tailscale.com/ipn"
)

type watcher struct {
	tailnet *Tailnet
	done    chan struct{}
}

func newWatcher(tailnet *Tailnet) *watcher {
	return &watcher{
		tailnet: tailnet,
		done:    make(chan struct{}),
	}
}

func (w *watcher) Start() {
	log.Println("Starting IPN watcher")
	go func() {
		// TODO: Do we need something like this?
		// Wait a moment for tsnet to initialize
		//time.Sleep(500 * time.Millisecond)

		lc, err := w.tailnet.server.LocalClient()
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
			select {
			case <-w.done:
				log.Printf("IPN watcher stopped")
				return
			default:
			}

			n, err := watcher.Next()
			if err != nil {
				log.Printf("IPN watcher error: %v", err)
				err := w.tailnet.State.SetFailed(err)
				if err != nil {
					log.Printf("failed to set state to failed: %v", err)
				}
				return
			}
			if n.State != nil {
				state := *n.State
				log.Printf("IPN state changed: %v", state)
				switch state {
				case ipn.Running:
					log.Printf("tsnet fully connected")
					// TODO: Guard against connecting to a different tailnet than we had before
					err := w.tailnet.State.SetConnected(n.NetMap.MagicDNSSuffix())
					if err != nil {
						log.Printf("failed to set state to connected: %v", err)
					}
				case ipn.NeedsLogin:
					log.Printf("tsnet needs login")
					// Get auth URL from status
					if status, err := lc.Status(ctx); err == nil && status.AuthURL != "" {
						log.Printf("Auth URL: %s", status.AuthURL)
						err := w.tailnet.State.SetNeedsLogin(status.AuthURL)
						if err != nil {
							log.Printf("failed to set state to needs login: %v", err)
						}
					}
				case ipn.NeedsMachineAuth:
					log.Printf("tsnet needs machine auth (admin approval)")
					// TODO: Check that we have the magicDNSSuffix here.
					// Can NetMap be nil here?
					err := w.tailnet.State.SetNeedsMachineAuth(n.NetMap.MagicDNSSuffix())
					if err != nil {
						log.Printf("failed to set state to needs machine auth: %v", err)
					}
				case ipn.Stopped:
					log.Printf("tsnet stopped/disconnected")
					// TODO: Is this the right state to set? When does this happen?
					err := w.tailnet.State.SetConnecting()
					if err != nil {
						log.Printf("failed to set state to connecting: %v", err)
					}
				case ipn.Starting:
					log.Printf("tsnet starting")
					err := w.tailnet.State.SetConnecting()
					if err != nil {
						log.Printf("failed to set state to connecting: %v", err)
					}
				}
			}
			// Also check for auth URL in BrowseToURL notifications
			if n.BrowseToURL != nil && *n.BrowseToURL != "" {
				log.Printf("Auth URL from notification: %s", *n.BrowseToURL)
				err := w.tailnet.State.SetNeedsLogin(*n.BrowseToURL)
				if err != nil {
					log.Printf("failed to set state to needs login: %v", err)
				}
			}
		}
	}()
}

func (w *watcher) Stop() {
	close(w.done)
}
