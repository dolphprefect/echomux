package api

import (
	"context"
	"testing"
	"time"

	"github.com/dolphprefect/echomux/internal/bluetooth"
	"nhooyr.io/websocket"
)

func TestRunEventForwarder_ClosedChannel(t *testing.T) {
	h := newHub()
	ch := make(chan bluetooth.Event)

	done := make(chan struct{})
	go func() {
		h.RunEventForwarder(context.Background(), ch)
		close(done)
	}()

	close(ch)

	select {
	case <-done:
		// RunEventForwarder exited cleanly when the channel was closed.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("RunEventForwarder did not exit after channel closed")
	}
}

func TestRunEventForwarder_ContextCancel(t *testing.T) {
	h := newHub()
	ch := make(chan bluetooth.Event)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		h.RunEventForwarder(ctx, ch)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("RunEventForwarder did not exit after context cancelled")
	}
}

// TestHubRemove verifies that remove deletes the connection from the clients map.
// A nil *websocket.Conn is used as the map key so that no real network I/O occurs;
// the map semantics are identical to a real *websocket.Conn pointer.
func TestHubRemove(t *testing.T) {
	h := newHub()
	var c *websocket.Conn // nil pointer — valid as a map key

	h.add(c)

	h.mu.Lock()
	before := len(h.clients)
	h.mu.Unlock()
	if before != 1 {
		t.Fatalf("expected 1 client after add, got %d", before)
	}

	h.remove(c)

	h.mu.Lock()
	after := len(h.clients)
	h.mu.Unlock()
	if after != 0 {
		t.Fatalf("expected 0 clients after remove, got %d", after)
	}
}
