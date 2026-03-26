package sse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewSSEBroadcaster(t *testing.T) {
	b := NewSSEBroadcaster()
	if b == nil {
		t.Fatal("expected non-nil broadcaster")
	}
}

func TestSSEBroadcaster_Subscribe(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx := context.Background()
	id, ch := b.Subscribe(ctx)
	if id == "" {
		t.Error("expected non-empty id")
	}
	if ch == nil {
		t.Error("expected non-nil channel")
	}
	b.Unsubscribe(id)
}

func TestSSEBroadcaster_SubscribeMultiple(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx := context.Background()
	id1, _ := b.Subscribe(ctx)
	id2, _ := b.Subscribe(ctx)
	if id1 == id2 {
		t.Error("expected unique subscriber IDs")
	}
	b.Unsubscribe(id1)
	b.Unsubscribe(id2)
}

func TestSSEBroadcaster_Unsubscribe(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx := context.Background()
	id, ch := b.Subscribe(ctx)
	b.Unsubscribe(id)
	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed")
	}
}

func TestSSEBroadcaster_UnsubscribeNonExistent(t *testing.T) {
	b := NewSSEBroadcaster()
	b.Unsubscribe("non-existent")
}

func TestSSEBroadcaster_Broadcast(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx := context.Background()
	id, ch := b.Subscribe(ctx)
	defer b.Unsubscribe(id)

	b.Broadcast("test-event")

	select {
	case event := <-ch:
		if event != "test-event" {
			t.Errorf("got %q, want %q", event, "test-event")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestSSEBroadcaster_BroadcastToMultiple(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx := context.Background()
	id1, ch1 := b.Subscribe(ctx)
	defer b.Unsubscribe(id1)
	id2, ch2 := b.Subscribe(ctx)
	defer b.Unsubscribe(id2)

	b.Broadcast("shared-event")

	for i, ch := range []<-chan string{ch1, ch2} {
		select {
		case event := <-ch:
			if event != "shared-event" {
				t.Errorf("subscriber %d: got %q, want %q", i, event, "shared-event")
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timeout", i)
		}
	}
}

func TestSSEBroadcaster_BroadcastNoSubscribers(t *testing.T) {
	b := NewSSEBroadcaster()
	b.Broadcast("orphan-event")
}

func TestSSEBroadcaster_BroadcastTailnetChange(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx := context.Background()
	id, ch := b.Subscribe(ctx)
	defer b.Unsubscribe(id)

	b.BroadcastTailnetChange(42)

	select {
	case event := <-ch:
		if event != "tailnet-42" {
			t.Errorf("got %q, want %q", event, "tailnet-42")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestSSEBroadcaster_BroadcastGlobalChange(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx := context.Background()
	id, ch := b.Subscribe(ctx)
	defer b.Unsubscribe(id)

	b.BroadcastGlobalChange()

	select {
	case event := <-ch:
		if event != "global" {
			t.Errorf("got %q, want %q", event, "global")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestSSEBroadcaster_ContextCancellation(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx, cancel := context.WithCancel(context.Background())
	_, ch := b.Subscribe(ctx)
	cancel()

	time.Sleep(50 * time.Millisecond)

	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed after context cancellation")
	}
}

func TestSSEBroadcaster_ConcurrentBroadcast(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx := context.Background()
	id, ch := b.Subscribe(ctx)
	defer b.Unsubscribe(id)

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Broadcast("event")
		}()
	}
	wg.Wait()

	received := 0
	for {
		select {
		case <-ch:
			received++
		default:
			if received == 0 {
				t.Error("expected to receive at least some events")
			}
			return
		}
	}
}

func TestSSEBroadcaster_ServeSSE(t *testing.T) {
	b := NewSSEBroadcaster()

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodGet, "/events", nil)
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		b.ServeSSE(w, r)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	b.Broadcast("test-event")
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not stop after context cancellation")
	}

	body := w.Body.String()

	if w.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", w.Header().Get("Cache-Control"))
	}
	if !strings.Contains(body, "event: global") {
		t.Error("expected initial global event in body")
	}
	if !strings.Contains(body, "event: test-event") {
		t.Error("expected test-event in body")
	}
}

func TestSSEBroadcaster_ImplementsBroadcaster(t *testing.T) {
	var _ Broadcaster = (*SSEBroadcaster)(nil)
}

func TestWriteSSEEvent(t *testing.T) {
	w := httptest.NewRecorder()
	writeSSEEvent(w, "my-event")

	body := w.Body.String()
	expected := "event: my-event\ndata: update\n\n"
	if body != expected {
		t.Errorf("got %q, want %q", body, expected)
	}
}

func TestSSEBroadcaster_ServeSSE_InitialGlobalRefresh(t *testing.T) {
	b := NewSSEBroadcaster()

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodGet, "/events", nil)
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		b.ServeSSE(w, r)
		close(done)
	}()

	// Cancel immediately — should still have the initial global event
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := w.Body.String()
	if !strings.Contains(body, "event: global\ndata: update\n\n") {
		t.Errorf("expected initial global refresh event, got %q", body)
	}
}

func TestSSEBroadcaster_BufferFull_DropsEvent(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx := context.Background()
	id, ch := b.Subscribe(ctx)
	defer b.Unsubscribe(id)

	// Fill the buffer (subscriberBufferSize = 10)
	for range subscriberBufferSize {
		b.Broadcast("fill")
	}

	// This broadcast should be dropped (buffer full) without blocking
	done := make(chan struct{})
	go func() {
		b.Broadcast("overflow")
		close(done)
	}()

	select {
	case <-done:
		// Good - didn't block
	case <-time.After(time.Second):
		t.Fatal("Broadcast blocked when buffer was full")
	}

	// Drain the channel
	received := 0
	for {
		select {
		case <-ch:
			received++
		default:
			goto done_drain
		}
	}
done_drain:

	if received != subscriberBufferSize {
		t.Errorf("received %d events, want %d (overflow should be dropped)", received, subscriberBufferSize)
	}
}

func TestSSEBroadcaster_DoubleUnsubscribe(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx := context.Background()
	id, _ := b.Subscribe(ctx)
	b.Unsubscribe(id)
	// Second unsubscribe should not panic
	b.Unsubscribe(id)
}
