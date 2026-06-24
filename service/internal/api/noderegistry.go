package api

import (
	"context"
	"sync"
	"time"
)

type nodeInfo struct {
	ID          string
	Name        string
	Addr        string             // host:port of satellite's HTTP API
	Online      bool
	LastSeen    time.Time
	RTPModuleID int                // pactl module ID; 0 = not loaded
	Devices     []deviceInfo       // cached from satellite WS push
	cancelConn  context.CancelFunc // cancels the active WS session goroutines
	nextSeq     int                // expected next delta event seq; reset to 0 on register
}

type nodeRegistry struct {
	mu    sync.Mutex
	nodes map[string]*nodeInfo // id → info
}

func newNodeRegistry() *nodeRegistry {
	return &nodeRegistry{nodes: make(map[string]*nodeInfo)}
}

// prepareRegistration checks whether a new connection for nodeID is acceptable.
// Returns (staleModuleID, accept):
//   - accept=false: duplicate name guard fired; caller must close the new connection.
//   - accept=true, staleModuleID!=0: stale session cleaned up; caller must call
//     audio.RemoveRTPSink(staleModuleID) outside the lock, then call commit.
func (r *nodeRegistry) prepareRegistration(id string) (staleModuleID int, accept bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n, exists := r.nodes[id]
	if !exists {
		return 0, true
	}
	if n.Online {
		// Duplicate name guard: live session exists — reject the new connection.
		return 0, false
	}
	// Stale session: cancel context (idempotent if already done), invalidate cache.
	if n.cancelConn != nil {
		n.cancelConn()
	}
	staleModuleID = n.RTPModuleID
	n.Online = false
	n.Devices = nil
	n.RTPModuleID = 0
	return staleModuleID, true
}

// commit writes the new session state for a satellite after prepareRegistration accepted it.
func (r *nodeRegistry) commit(id, name, addr string, rtpModuleID int, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, exists := r.nodes[id]
	if !exists {
		n = &nodeInfo{}
		r.nodes[id] = n
	}
	n.ID = id
	n.Name = name
	n.Addr = addr
	n.Online = true
	n.LastSeen = time.Now()
	n.RTPModuleID = rtpModuleID
	n.cancelConn = cancel
	n.nextSeq = 0
	n.Devices = nil
}

// setOffline marks a node offline and clears its device cache.
func (r *nodeRegistry) setOffline(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n, ok := r.nodes[id]; ok {
		n.Online = false
		n.Devices = nil
	}
}

// setDevices replaces the full device cache for a node and resets the seq
// acceptance state so the next delta event is accepted regardless of its seq.
func (r *nodeRegistry) setDevices(id string, devices []deviceInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		return
	}
	n.Devices = devices
	n.nextSeq = -1 // accept-any: satellite continues from its own counter
}

// applyDeltaEvent applies a connected/disconnected delta to the device cache.
// Returns true if a seq gap was detected (caller must send request_sync).
func (r *nodeRegistry) applyDeltaEvent(id string, mac, event string, seq int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		return false
	}
	// Seq check: nextSeq==-1 means accept-any (after a full push).
	if n.nextSeq != -1 && seq != n.nextSeq {
		return true // gap detected
	}
	if n.nextSeq != -1 {
		n.nextSeq++
	} else {
		n.nextSeq = seq + 1
	}
	connected := event == "connected"
	for i := range n.Devices {
		if n.Devices[i].MAC == mac {
			n.Devices[i].Connected = connected
			return false
		}
	}
	return false
}

// list returns a snapshot of all nodeInfo entries.
// Devices is deep-copied so callers can safely iterate it without holding the lock
// while applyDeltaEvent concurrently mutates the live slice elements.
func (r *nodeRegistry) list() []*nodeInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*nodeInfo, 0, len(r.nodes))
	for _, n := range r.nodes {
		cp := *n
		if len(n.Devices) > 0 {
			cp.Devices = make([]deviceInfo, len(n.Devices))
			copy(cp.Devices, n.Devices)
		}
		out = append(out, &cp)
	}
	return out
}
