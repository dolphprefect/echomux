package api

import (
	"context"
	"sync"

	"github.com/dolphprefect/echomux/internal/bluetooth"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type hubSub struct {
	ch chan any
}

type hub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]struct{}
	subs    []*hubSub
}

func newHub() *hub {
	return &hub{clients: make(map[*websocket.Conn]struct{})}
}

func (h *hub) add(c *websocket.Conn) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *hub) remove(c *websocket.Conn) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// Subscribe returns a channel that receives a copy of every broadcasted message.
// The caller must invoke the returned cancel func to unsubscribe and release
// the channel; undelivered messages are dropped when the subscriber is slow.
func (h *hub) Subscribe(bufSize int) (<-chan any, func()) {
	sub := &hubSub{ch: make(chan any, bufSize)}
	h.mu.Lock()
	h.subs = append(h.subs, sub)
	h.mu.Unlock()
	return sub.ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		for i, s := range h.subs {
			if s == sub {
				h.subs[i] = h.subs[len(h.subs)-1]
				h.subs = h.subs[:len(h.subs)-1]
				return
			}
		}
	}
}

func (h *hub) broadcast(ctx context.Context, msg any) {
	h.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(h.clients))
	for c := range h.clients {
		conns = append(conns, c)
	}
	subs := make([]*hubSub, len(h.subs))
	copy(subs, h.subs)
	h.mu.Unlock()
	for _, c := range conns {
		_ = wsjson.Write(ctx, c, msg)
	}
	for _, sub := range subs {
		select {
		case sub.ch <- msg:
		default: // drop if subscriber is slow
		}
	}
}

// RunEventForwarder reads BT events and fans them out to WS clients.
func (h *hub) RunEventForwarder(ctx context.Context, events <-chan bluetooth.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			h.broadcast(ctx, map[string]string{"mac": ev.MAC, "type": ev.Type})
		}
	}
}
