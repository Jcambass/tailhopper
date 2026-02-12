package ts

import (
	"context"
	"sync"

	"github.com/jcambass/tailhopper/internal/logging"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
)

type watcher struct {
	tailnet       *Tailnet
	ipnBusWatcher *local.IPNBusWatcher
	wg            *sync.WaitGroup
	cancel        context.CancelFunc
	logger        *logging.Logger
}

func newWatcher(tailnet *Tailnet) *watcher {
	return &watcher{
		tailnet: tailnet,
		wg:      &sync.WaitGroup{},
		logger:  tailnet.logger.WithFields(map[string]string{"component": "watcher", "job": "ipn"}),
	}
}

func (w *watcher) Start() {
	w.logger.Printf("Starting IPN watcher")
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	ctx = logging.WithContext(ctx, w.logger)

	w.wg.Go(func() {
		defer logging.CatchPanic(w.logger)
		defer w.logger.Printf("IPN watcher goroutine exiting")

		// TODO: Do we need something like this?
		// Wait a moment for tsnet to initialize
		//time.Sleep(500 * time.Millisecond)

		lc, err := w.tailnet.server.LocalClient()
		if err != nil {
			w.logger.Printf("failed to get LocalClient for watcher: %v", err)
			return
		}

		// TODO: Use NotifyWatchEngineUpdates?
		// TODO: Use NotifyInitialHealthState?
		// Request both initial state and netmap to ensure we get state transitions immediately
		watcher, err := lc.WatchIPNBus(ctx, ipn.NotifyInitialState|ipn.NotifyInitialNetMap)
		if err != nil {
			w.logger.Printf("failed to watch IPN bus: %v", err)
			return
		}
		w.ipnBusWatcher = watcher
		defer watcher.Close()

		for {
			n, err := watcher.Next()
			if err != nil {
				w.logger.Printf("IPN watcher error: %v", err)
				// The watcher can close due to tailnet shutdown; ignore and exit.
				// Ideally we could distinguish between expected closure and unexpected errors.

				return
			}

			w.logger.Printf("Received IPN notification: %s", n.String())
			w.tailnet.UpdateLatestState(&n)
			w.logger.Printf("Updated tailnet state: %s", w.tailnet.LatestState().String())
		}
	})
}

func (w *watcher) Stop() {
	if w.ipnBusWatcher != nil {
		// TODO: Are all these needed?
		w.logger.Printf("Canceling IPN watcher ctx")
		w.cancel()
		w.cancel = nil
		w.logger.Printf("Closing IPN watcher")
		w.ipnBusWatcher.Close()
		w.ipnBusWatcher = nil
		w.logger.Printf("IPN watcher closed, waiting for goroutine to exit")
		w.wg.Wait()
		w.logger.Printf("IPN watcher stopped successfully")
	}
}
