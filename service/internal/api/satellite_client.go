package api

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	backoffBase   = 1 * time.Second
	backoffCap    = 30 * time.Second
	backoffJitter = 0.15
	satRegTimeout = 10 * time.Second
)

// backoffDelay returns the reconnect delay for attempt (0-indexed).
// Base doubles each attempt (1s, 2s, 4s…) capped at 30 s; ±15% jitter
// is applied to prevent thundering-herd reconnects after a power outage.
func backoffDelay(attempt int, rng *rand.Rand) time.Duration {
	if attempt > 30 {
		attempt = 30
	}
	base := backoffBase << uint(attempt)
	if base > backoffCap || base <= 0 { // guard against int64 overflow
		base = backoffCap
	}
	jitterRange := float64(base) * backoffJitter
	delta := time.Duration((rng.Float64()*2 - 1) * jitterRange)
	d := base + delta
	if d < 0 {
		d = 0
	}
	return d
}

// runSatelliteClient is the top-level goroutine that keeps the satellite
// connected to the master, reconnecting with exponential backoff on failure.
func (s *server) runSatelliteClient(ctx context.Context) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	attempt := 0
	for {
		registered, err := s.satelliteSessionLoop(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("satellite: session ended: %v", err)
		}
		if registered {
			attempt = 0 // reset backoff after a successful session
		} else {
			attempt++
		}
		delay := backoffDelay(attempt, rng)
		log.Printf("satellite: reconnecting in %v", delay.Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// satelliteSessionLoop dials the master, registers, and runs one session until
// the connection drops. Returns (registered=true) if the session made it past
// the registration handshake (used to reset the backoff counter).
func (s *server) satelliteSessionLoop(ctx context.Context) (registered bool, _ error) {
	url := "ws://" + s.masterAddr + "/nodes"
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return false, fmt.Errorf("dial %s: %w", url, err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(wsReadLimit)

	events, unsub := s.hub.Subscribe(64)
	defer unsub()

	writeMu := &sync.Mutex{}

	if err := writeNode(ctx, conn, writeMu, nodeWsMsg{
		Type: "register",
		Name: s.name,
		Addr: s.selfAddr,
	}); err != nil {
		return false, fmt.Errorf("register send: %w", err)
	}

	var ack nodeWsMsg
	regCtx, regCancel := context.WithTimeout(ctx, satRegTimeout)
	err = wsjson.Read(regCtx, conn, &ack)
	regCancel()
	if err != nil {
		return false, fmt.Errorf("register ack read: %w", err)
	}
	if ack.Type != "registered" {
		return false, fmt.Errorf("expected registered, got %q", ack.Type)
	}

	if err := s.satSendDevices(ctx, conn, writeMu); err != nil {
		return false, fmt.Errorf("initial device push: %w", err)
	}

	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	go s.satEventForwarder(connCtx, connCancel, conn, writeMu, events)

	return true, s.satReadLoop(connCtx, connCancel, conn, writeMu)
}

// satSendDevices fetches the current BT device list and pushes it as a full
// "devices" snapshot to the master.
func (s *server) satSendDevices(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex) error {
	devs, err := s.bt.Devices(ctx)
	if err != nil {
		return fmt.Errorf("bt.Devices: %w", err)
	}

	s.mu.Lock()
	items := make([]deviceInfo, 0)
	var pendingIdxs []int // indices into items where volume is unresolved
	for _, d := range devs {
		if s.knownSpeakers[d.MAC] {
			vol, hasVol := s.volumes[d.MAC]
			if !hasVol {
				pendingIdxs = append(pendingIdxs, len(items))
			}
			items = append(items, deviceInfo{
				Device:  d,
				DelayMs: s.delays[d.MAC],
				Volume:  vol,
				Muted:   s.mutes[d.MAC],
				Playing: !speakerDead(s.speakers[d.MAC]),
			})
		}
	}
	s.mu.Unlock()

	// Resolve actual PW volumes for devices with no stored volume.
	if len(pendingIdxs) > 0 {
		nodes, err := s.audio.Nodes(ctx)
		if err == nil {
			nodeByMAC := make(map[string]int, len(nodes))
			for _, n := range nodes {
				nodeByMAC[n.MAC] = n.ID
			}
			for _, idx := range pendingIdxs {
				mac := items[idx].MAC
				if nodeID, ok := nodeByMAC[mac]; ok {
					if v, err := s.audio.GetVolume(ctx, nodeID); err == nil && v >= 0 {
						items[idx].Volume = v
						continue
					}
				}
				items[idx].Volume = -1 // PipeWire node not yet created
			}
		}
	}

	return writeNode(ctx, conn, writeMu, nodeWsMsg{Type: "devices", Devices: items})
}

// satEventForwarder reads from the hub subscription and forwards BT device
// events (connected/disconnected) to the master as seq-numbered delta events.
// Only map[string]string messages from RunEventForwarder are forwarded.
// It also listens to s.satPushCh to trigger full device list updates on state changes.
func (s *server) satEventForwarder(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, writeMu *sync.Mutex, events <-chan any) {
	seq := 0
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-events:
			if !ok {
				return
			}
			ev, ok := msg.(map[string]string)
			if !ok {
				continue
			}
			evType := ev["type"]
			if evType != "connected" && evType != "disconnected" {
				continue
			}
			if err := writeNode(ctx, conn, writeMu, nodeWsMsg{
				Type:  "event",
				MAC:   ev["mac"],
				Event: evType,
				Seq:   seq,
			}); err != nil {
				cancel()
				return
			}
			seq++
		case <-s.satPushCh:
			if err := s.satSendDevices(ctx, conn, writeMu); err != nil {
				cancel()
				return
			}
		}
	}
}

// satReadLoop reads messages from the master and handles ping→pong and
// request_sync. Returns when the connection closes or ctx is cancelled.
func (s *server) satReadLoop(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, writeMu *sync.Mutex) error {
	for {
		var msg nodeWsMsg
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			return err
		}
		switch msg.Type {
		case "ping":
			if err := writeNode(ctx, conn, writeMu, nodeWsMsg{Type: "pong"}); err != nil {
				cancel()
				return err
			}
		case "request_sync":
			if err := s.satSendDevices(ctx, conn, writeMu); err != nil {
				cancel()
				return err
			}
		}
	}
}
