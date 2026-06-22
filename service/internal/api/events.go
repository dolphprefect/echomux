package api

import (
	"context"
	"sync"

	"github.com/dolphprefect/echomux/internal/bluetooth"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type hub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]struct{}
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

func (h *hub) broadcast(ctx context.Context, msg any) {
	h.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(h.clients))
	for c := range h.clients {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		_ = wsjson.Write(ctx, c, msg)
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
