package api

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	wsSendTimeout   = 5 * time.Second
	fullPushInterval = 5 * time.Minute
	wsReadLimit     = 512 * 1024 // 512 KiB
)

// nodeWsMsg is the JSON message format on the /nodes WS control channel.
type nodeWsMsg struct {
	Type    string       `json:"type"`
	ID      string       `json:"id,omitempty"`
	Name    string       `json:"name,omitempty"`
	Addr    string       `json:"addr,omitempty"`
	Devices []deviceInfo `json:"devices,omitempty"`
	MAC     string       `json:"mac,omitempty"`
	Event   string       `json:"event,omitempty"`
	Seq     int          `json:"seq,omitempty"`
}

// nodeListItem is the shape returned by GET /nodes.
type nodeListItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	Online bool   `json:"online"`
	Addr   string `json:"addr"`
}

// slugify converts a display name to a URL-safe node ID.
func slugify(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", "-"))
}

// writeNode sends a JSON message with a 5 s write deadline.
func writeNode(ctx context.Context, conn *websocket.Conn, msg nodeWsMsg) error {
	ctx, cancel := context.WithTimeout(ctx, wsSendTimeout)
	defer cancel()
	return wsjson.Write(ctx, conn, msg)
}

// handleNodes routes /nodes to the WS satellite handler (Upgrade) or the REST handler.
func (s *server) handleNodes(w http.ResponseWriter, r *http.Request) {
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		if s.mode != ModeMaster {
			http.Error(w, "only master mode accepts satellite connections", http.StatusForbidden)
			return
		}
		s.handleSatelliteConn(w, r)
		return
	}
	s.handleGetNodes(w, r)
}

// handleGetNodes implements GET /nodes (REST).
func (s *server) handleGetNodes(w http.ResponseWriter, r *http.Request) {
	masterID := slugify(s.name)
	items := []nodeListItem{
		{ID: masterID, Name: s.name, Role: "master", Online: true, Addr: ""},
	}
	if s.nodes != nil {
		for _, n := range s.nodes.list() {
			items = append(items, nodeListItem{
				ID:     n.ID,
				Name:   n.Name,
				Role:   "satellite",
				Online: n.Online,
				Addr:   n.Addr,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

// handleSatelliteConn manages one satellite WebSocket session on /nodes.
func (s *server) handleSatelliteConn(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(wsReadLimit)

	// First message must be "register".
	var regMsg nodeWsMsg
	readCtx, readCancel := context.WithTimeout(r.Context(), 10*time.Second)
	err = wsjson.Read(readCtx, conn, &regMsg)
	readCancel()
	if err != nil || regMsg.Type != "register" || regMsg.Name == "" {
		conn.Close(websocket.StatusProtocolError, "expected register{name,addr}")
		return
	}

	nodeID := slugify(regMsg.Name)

	// Dirty reconnect / duplicate name guard.
	staleModuleID, accept := s.nodes.prepareRegistration(nodeID)
	if !accept {
		conn.Close(websocket.StatusPolicyViolation, "duplicate satellite name: "+regMsg.Name)
		return
	}
	if staleModuleID != 0 {
		go func() { _ = s.audio.RemoveRTPSink(context.Background(), staleModuleID) }()
	}

	// Provision RTP unicast sink from Master to Satellite.
	satIP, _, _ := net.SplitHostPort(regMsg.Addr)
	var rtpModuleID int
	if satIP != "" {
		rtpModuleID, err = s.audio.AddRTPSink(r.Context(), satIP, s.rtpPort)
		if err != nil {
			log.Printf("satellite %s: AddRTPSink %s:%d: %v", regMsg.Name, satIP, s.rtpPort, err)
		}
	}

	// Create a per-session context cancelled when this session ends.
	connCtx, connCancel := context.WithCancel(r.Context())

	s.nodes.commit(nodeID, regMsg.Name, regMsg.Addr, rtpModuleID, connCancel)

	if err := writeNode(connCtx, conn, nodeWsMsg{Type: "registered", ID: nodeID}); err != nil {
		connCancel()
		// satellite_online was not broadcast yet — clean up silently (no offline event).
		s.nodes.setOffline(nodeID)
		if rtpModuleID != 0 {
			go func() { _ = s.audio.RemoveRTPSink(context.Background(), rtpModuleID) }()
		}
		return
	}

	go s.hub.broadcast(context.Background(), map[string]any{
		"type":    "satellite_online",
		"node_id": nodeID,
		"name":    regMsg.Name,
	})

	// Run heartbeat and periodic full-push timer concurrently.
	pongCh := make(chan struct{}, 1)
	go s.satelliteHeartbeat(connCtx, connCancel, conn, pongCh, nodeID)
	go s.satelliteSyncTimer(connCtx, conn)

	s.satelliteReadLoop(connCtx, conn, nodeID, pongCh)

	connCancel()
	s.endSatelliteSession(nodeID, rtpModuleID, regMsg.Name)
}

func (s *server) satelliteHeartbeat(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, pongCh <-chan struct{}, nodeID string) {
	ticker := time.NewTicker(s.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := writeNode(ctx, conn, nodeWsMsg{Type: "ping"}); err != nil {
				cancel()
				return
			}
			watchdog := time.NewTimer(s.pongTimeout)
			select {
			case <-ctx.Done():
				watchdog.Stop()
				return
			case <-pongCh:
				watchdog.Stop()
			case <-watchdog.C:
				log.Printf("satellite %s: pong timeout — closing connection", nodeID)
				conn.CloseNow()
				cancel()
				return
			}
		}
	}
}

func (s *server) satelliteSyncTimer(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(fullPushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = writeNode(ctx, conn, nodeWsMsg{Type: "request_sync"})
		}
	}
}

func (s *server) satelliteReadLoop(ctx context.Context, conn *websocket.Conn, nodeID string, pongCh chan<- struct{}) {
	for {
		var msg nodeWsMsg
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			return
		}
		switch msg.Type {
		case "pong":
			select {
			case pongCh <- struct{}{}:
			default:
			}
		case "devices":
			s.nodes.setDevices(nodeID, msg.Devices)
		case "event":
			gap := s.nodes.applyDeltaEvent(nodeID, msg.MAC, msg.Event, msg.Seq)
			if gap {
				_ = writeNode(ctx, conn, nodeWsMsg{Type: "request_sync"})
			} else {
				go s.hub.broadcast(ctx, map[string]any{
					"type":    msg.Event,
					"mac":     msg.MAC,
					"node_id": nodeID,
				})
			}
		}
	}
}

func (s *server) endSatelliteSession(nodeID string, rtpModuleID int, name string) {
	s.nodes.setOffline(nodeID)
	if rtpModuleID != 0 {
		go func() { _ = s.audio.RemoveRTPSink(context.Background(), rtpModuleID) }()
	}
	go s.hub.broadcast(context.Background(), map[string]any{
		"type":    "satellite_offline",
		"node_id": nodeID,
		"name":    name,
	})
}
