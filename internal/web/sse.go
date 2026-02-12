package web

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/jcambass/tailhopper/internal/logging"
)

// SSEBroadcaster manages Server-Sent Events subscriptions and broadcasts.
type SSEBroadcaster struct {
	mu          sync.RWMutex
	subscribers map[string]chan string
	logger      *logging.Logger
	nextClient  int
}

// NewSSEBroadcaster creates a new SSE broadcaster.
func NewSSEBroadcaster(logger *logging.Logger) *SSEBroadcaster {
	return &SSEBroadcaster{
		subscribers: make(map[string]chan string),
		logger:      logger.With("component", "sse"),
		nextClient:  1,
	}
}

// Subscribe creates a new subscription and returns a channel that receives events.
func (b *SSEBroadcaster) Subscribe(ctx context.Context) (string, <-chan string) {
	b.mu.Lock()
	id := fmt.Sprintf("client-%d", b.nextClient)
	b.nextClient++
	ch := make(chan string, 10) // Buffer to prevent blocking
	b.subscribers[id] = ch
	b.mu.Unlock()

	b.logger.Printf("New SSE subscriber: %s (total: %d)", id, len(b.subscribers))

	// Clean up on context cancellation
	go func() {
		<-ctx.Done()
		b.Unsubscribe(id)
	}()

	return id, ch
}

// Unsubscribe removes a subscription.
func (b *SSEBroadcaster) Unsubscribe(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if ch, ok := b.subscribers[id]; ok {
		close(ch)
		delete(b.subscribers, id)
		b.logger.Printf("SSE subscriber disconnected: %s (remaining: %d)", id, len(b.subscribers))
	}
}

// Broadcast sends an event to all subscribers.
func (b *SSEBroadcaster) Broadcast(event string) {
	b.mu.RLock()
	if len(b.subscribers) == 0 {
		b.mu.RUnlock()
		return
	}

	subs := make([]chan string, 0, len(b.subscribers))
	for _, ch := range b.subscribers {
		subs = append(subs, ch)
	}
	count := len(subs)
	b.mu.RUnlock()

	b.logger.Printf("Broadcasting SSE event to %d subscribers", count)

	for idx, ch := range subs {
		select {
		case ch <- event:
			// Successfully sent
		default:
			// Channel is full, skip this subscriber
			b.logger.Printf("SSE subscriber %d channel full, skipping event", idx)
		}
	}
}

// NotifyStateChange broadcasts a state change event for a specific tailnet.
// If tailnetID is empty, it broadcasts a global change event.
func (b *SSEBroadcaster) NotifyStateChange(tailnetID string) {
	if tailnetID == "" {
		b.Broadcast("global")
	} else {
		b.Broadcast(fmt.Sprintf("tailnet-%s", tailnetID))
	}
}

// NotifyGlobalChange broadcasts a global change event (e.g., tailnet add/remove).
func (b *SSEBroadcaster) NotifyGlobalChange() {
	b.Broadcast("global")
}

// ServeSSE handles SSE HTTP requests.
func (b *SSEBroadcaster) ServeSSE(w http.ResponseWriter, r *http.Request) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Create subscription
	clientID, eventChan := b.Subscribe(r.Context())
	defer b.Unsubscribe(clientID)

	// Always force a global refresh on connect to sync state
	writeSSEEvent(w, "global")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Stream events to client
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-eventChan:
			if !ok {
				return
			}
			writeSSEEvent(w, event)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, name string) {
	fmt.Fprintf(w, "event: %s\ndata: update\n\n", name)
}
