package ts

import (
	"context"
	"sync"

	"github.com/jcambass/tailhopper/internal/logging"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
)

type watcher struct {
	tailnet *Tailnet
	watcher *local.IPNBusWatcher
	wg      *sync.WaitGroup
}

func newWatcher(tailnet *Tailnet) *watcher {
	return &watcher{
		tailnet: tailnet,
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
		w.watcher = watcher
		defer watcher.Close()

		for {
			n, err := watcher.Next()
			if err != nil {
				logger.Printf("IPN watcher error: %v", err)
				// The watcher can close due to tailnet shutdown; ignore and exit.
				// Ideally we could distinguish between expected closure and unexpected errors.

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
		}
	})
}

func (w *watcher) Stop() {
	logger := w.tailnet.logger.WithFields(map[string]string{
		"component": "watcher",
		"job":       "ipn",
	})

	if w.watcher != nil {
		logger.Printf("Closing IPN watcher")
		w.watcher.Close()
		w.watcher = nil
		logger.Printf("IPN watcher closed, waiting for goroutine to exit")
		w.wg.Wait()
		logger.Printf("IPN watcher stopped successfully")
	}
}
