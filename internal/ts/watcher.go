package ts

import (
	"context"
	"sync"

	"github.com/jcambass/tailhopper/internal/logging"
	"tailscale.com/ipn"
)

type watcher struct {
	tailnet *Tailnet
	done    chan struct{}
	wg      *sync.WaitGroup
}

func newWatcher(tailnet *Tailnet) *watcher {
	return &watcher{
		tailnet: tailnet,
		done:    make(chan struct{}),
		wg:      &sync.WaitGroup{},
	}
}

func (w *watcher) Start() {
	logger := w.tailnet.logger.WithFields(map[string]string{
		"component": "watcher",
		"job":       "ipn",
	})
	logger.Printf("Starting IPN watcher")
	w.wg.Go(func() {
		defer logging.CatchPanic(logger)
		defer logger.Printf("IPN watcher goroutine exiting")

		ctx := logging.WithContext(context.Background(), logger)
		// TODO: Do we need something like this?
		// Wait a moment for tsnet to initialize
		//time.Sleep(500 * time.Millisecond)

		lc, err := w.tailnet.server.LocalClient()
		if err != nil {
			logger.Printf("failed to get LocalClient for watcher: %v", err)
			return
		}

		// TODO: Use NotifyWatchEngineUpdates?
		// TODO: Use NotifyInitialHealthState?
		watcher, err := lc.WatchIPNBus(ctx, ipn.NotifyInitialState|ipn.NotifyInitialNetMap)
		if err != nil {
			logger.Printf("failed to watch IPN bus: %v", err)
			return
		}
		defer watcher.Close()

		for {
			select {
			case <-w.done:
				logger.Printf("IPN watcher stopped")
				return
			default:
			}

			n, err := watcher.Next()
			if err != nil {
				logger.Printf("IPN watcher error: %v", err)
				w.tailnet.State.SetFailed(ctx, "ipn_watcher_error", err)
				return
			}
			if n.State != nil {
				state := *n.State
				logger.Printf("IPN state changed: %v", state)
				_, err := w.tailnet.RefreshState(ctx)
				if err != nil {
					logger.Printf("watcher failed to refresh state: %v", err)
				}
			}
			// Also check for auth URL in BrowseToURL notifications
			// if n.BrowseToURL != nil && *n.BrowseToURL != "" {
			// 	logger.Printf("Auth URL from notification: %s", *n.BrowseToURL)
			// 	w.tailnet.State.SetNeedsLogin(ctx, *n.BrowseToURL)
			// }
		}
	})
}

func (w *watcher) Stop() {
	close(w.done)
	w.wg.Wait()
}
