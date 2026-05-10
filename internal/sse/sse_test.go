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

func TestSSEBroadcaster_SubscribeAndBroadcast(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, ch := b.Subscribe(ctx)

	b.Broadcast("test-event")

	select {
	case event := <-ch:
		if event != "test-event" {
			t.Errorf("got %q, want %q", event, "test-event")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for broadcast")
	}
}

func TestSSEBroadcaster_Unsubscribe(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	id, ch := b.Subscribe(ctx)
	b.Unsubscribe(id)

	// Channel should be closed
	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed after unsubscribe")
	}
}

func TestSSEBroadcaster_ContextCancelUnsubscribes(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx, cancel := context.WithCancel(context.Background())

	_, ch := b.Subscribe(ctx)
	cancel()

	// Wait for goroutine to clean up
	deadline := time.After(time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for auto-unsubscribe")
		default:
		}
		_, ok := <-ch
		if !ok {
			break
		}
	}
}

func TestSSEBroadcaster_MultipleSubscribers(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, ch1 := b.Subscribe(ctx)
	_, ch2 := b.Subscribe(ctx)

	b.Broadcast("multi")

	for i, ch := range []<-chan string{ch1, ch2} {
		select {
		case event := <-ch:
			if event != "multi" {
				t.Errorf("subscriber %d: got %q, want %q", i, event, "multi")
			}
		case <-time.After(time.Second):
			t.Errorf("subscriber %d: timeout", i)
		}
	}
}

func TestSSEBroadcaster_BroadcastTailnetChange(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, ch := b.Subscribe(ctx)

	b.BroadcastTailnetChange(42)

	select {
	case event := <-ch:
		if event != "tailnet-42" {
			t.Errorf("got %q, want %q", event, "tailnet-42")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestSSEBroadcaster_BroadcastGlobalChange(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, ch := b.Subscribe(ctx)

	b.BroadcastGlobalChange()

	select {
	case event := <-ch:
		if event != "global" {
			t.Errorf("got %q, want %q", event, "global")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestSSEBroadcaster_NoSubscribers(t *testing.T) {
	b := NewSSEBroadcaster()
	// Should not panic when broadcasting with no subscribers
	b.Broadcast("nobody-listening")
}

func TestSSEBroadcaster_FullChannelDropsEvent(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, ch := b.Subscribe(ctx)

	// Fill the buffer
	for i := 0; i < subscriberBufferSize; i++ {
		b.Broadcast("fill")
	}

	// This one should be dropped (channel full)
	b.Broadcast("overflow")

	// Drain the buffer
	count := 0
	for {
		select {
		case <-ch:
			count++
		default:
			goto done
		}
	}
done:
	if count != subscriberBufferSize {
		t.Errorf("expected %d events, got %d", subscriberBufferSize, count)
	}
}

func TestSSEBroadcaster_ConcurrentBroadcast(t *testing.T) {
	b := NewSSEBroadcaster()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b.Subscribe(ctx)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Broadcast("concurrent")
		}()
	}
	wg.Wait()
}

func TestSSEBroadcaster_ServeSSE_SendsInitialGlobal(t *testing.T) {
	b := NewSSEBroadcaster()

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		b.ServeSSE(w, req)
		close(done)
	}()

	// Give the handler time to write the initial event
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := w.Body.String()
	if !strings.Contains(body, "event: global") {
		t.Errorf("expected initial global event, got %q", body)
	}
}
