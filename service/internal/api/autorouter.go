package api

import (
	"bytes"
	"context"
	"os/exec"
	"time"

	"github.com/dolphprefect/echomux/internal/audio"
)

// killOrphanLoopbacks kills any pw-loopback processes left over from a previous
// echomux instance. Called once at startup before the autorouter begins ticking,
// so orphans do not mix their undelayed streams with the newly-spawned ones.
func killOrphanLoopbacks() {
	// -x matches the process name exactly so we don't accidentally hit something like
	// pw-loopback-helper. -KILL (SIGKILL) is used because SIGTERM can be ignored.
	_ = exec.Command("pkill", "-KILL", "-x", "pw-loopback").Run()
	// Give the kernel a moment to reap the processes before the first tick spawns new ones.
	time.Sleep(200 * time.Millisecond)
}

type speakerState struct {
	nodeName  string
	cmd       *exec.Cmd
	dead      chan struct{} // closed when the process exits
	startedAt time.Time
}

// startupConnect tries to connect each known speaker after the BT/PW stack
// has had time to settle. Runs once at service start; errors are non-fatal.
func (s *server) startupConnect(ctx context.Context) {
	time.Sleep(8 * time.Second)

	s.mu.Lock()
	macs := make([]string, 0, len(s.knownSpeakers))
	for mac := range s.knownSpeakers {
		macs = append(macs, mac)
	}
	s.mu.Unlock()

	for _, mac := range macs {
		s.dbg("startup: connecting known speaker %s", mac)
		if err := s.bt.Connect(ctx, mac); err != nil {
			s.dbg("startup: connect %s failed: %v", mac, err)
		}
		time.Sleep(2 * time.Second)
	}
}

func (s *server) startAutoRouter(ctx context.Context, interval time.Duration) {
	killOrphanLoopbacks()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		defer s.killAllLoopbacks()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tickRouter(ctx)
			}
		}
	}()
}

// tickRouter links rtp-source → main-mix and ensures each connected BT speaker
// has a live pw-loopback process reading from main-mix-source.
func (s *server) tickRouter(ctx context.Context) {
	s.mu.Lock()
	paused := s.paused
	s.mu.Unlock()
	if paused {
		return
	}

	snap, err := s.audio.Snapshot(ctx)
	if err != nil {
		return
	}

	s.ensureMainMixLink(ctx, snap)

	current := make(map[string]string, len(snap.Nodes)) // MAC → nodeName
	nodeIDByMAC := make(map[string]int, len(snap.Nodes)) // MAC → PW node ID
	for _, n := range snap.Nodes {
		current[n.MAC] = n.Name
		nodeIDByMAC[n.MAC] = n.ID
	}

	if s.debug {
		names := make([]string, 0, len(current))
		for mac, n := range current {
			names = append(names, mac+"="+n)
		}
		s.dbg("tick: BT nodes found: %v", names)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Register any newly seen BT A2DP sinks as known speakers.
	needsSave := false
	for mac := range current {
		if !s.knownSpeakers[mac] {
			s.dbg("tick: new known speaker %s", mac)
			s.knownSpeakers[mac] = true
			needsSave = true
		}
	}

	// Kill loopbacks for speakers that are no longer connected.
	for mac := range s.speakers {
		if _, ok := current[mac]; !ok {
			s.dbg("tick: stopping loopback for gone speaker %s", mac)
			s.stopLoopback(mac)
		}
	}

	// Create or restart loopbacks for connected speakers.
	// Also restart if the PW node name changed (e.g. bluez_output.MAC.1 → .2 after reconnect).
	for mac, nodeName := range current {
		if nodeName == "" {
			// PW node is initialising and doesn't have its name yet; wait for the next tick.
			s.dbg("tick: skipping %s — node name not yet assigned", mac)
			continue
		}
		sp := s.speakers[mac]
		if speakerDead(sp) {
			s.dbg("tick: starting loopback for %s (%s)", mac, nodeName)
			s.startLoopback(mac, nodeName)
		} else if sp.nodeName != nodeName {
			s.dbg("tick: restarting loopback for %s: node changed %s → %s", mac, sp.nodeName, nodeName)
			s.stopLoopback(mac)
			s.startLoopback(mac, nodeName)
		}
	}

	// Zombie watchdog: if any PW link into a BT sink is not "active" while
	// its loopback has been running past the grace period, restart the loopback.
	// Links start in "paused" during format negotiation, so we skip recently
	// started loopbacks. A 30s cooldown between watchdog restarts prevents a
	// tight restart loop when the sink is genuinely unresponsive.
	if len(snap.Links) > 0 {
		nonActive := make(map[int]string) // BT node ID → first non-active link state
		for _, l := range snap.Links {
			if l.State != "active" {
				if _, seen := nonActive[l.InputNodeID]; !seen {
					nonActive[l.InputNodeID] = l.State
				}
			}
		}
		for mac, nodeName := range current {
			sp := s.speakers[mac]
			if speakerDead(sp) || time.Since(sp.startedAt) < 5*time.Second {
				continue
			}
			nodeID := nodeIDByMAC[mac]
			if nodeID == 0 {
				continue // zero is the PW Core object; skip to avoid false match
			}
			if time.Since(s.watchdogRestarts[mac]) < 30*time.Second {
				continue // cooldown: don't hammer a repeatedly-failing sink
			}
			if state, bad := nonActive[nodeID]; bad {
				s.dbg("watchdog: %s (node %d) link in state %q — restarting zombie loopback", mac, nodeID, state)
				s.watchdogRestarts[mac] = time.Now()
				s.stopLoopback(mac)
				s.startLoopback(mac, nodeName)
			}
		}
	}

	if needsSave {
		s.saveStateDebounced()
	}
}

// ensureMainMixLink creates the rtp-source → main-mix link if not already present.
// pw-link is idempotent on duplicates (returns error which we ignore).
// snap must come from the same Snapshot as the tick to avoid extra pw-dump calls.
func (s *server) ensureMainMixLink(ctx context.Context, snap audio.Snapshot) {
	mainMixID, ok := snap.NodesByName["main-mix"]
	if !ok {
		s.dbg("ensureMainMixLink: main-mix not found")
		return
	}
	for _, src := range snap.Sources {
		if src.Name == "rtp-source" {
			s.dbg("ensureMainMixLink: linking rtp-source(id=%d) → main-mix(id=%d)", src.ID, mainMixID)
			if lerr := s.audio.Link(ctx, src.ID, mainMixID); lerr != nil {
				s.dbg("ensureMainMixLink: pw-link error (likely already linked): %v", lerr)
			}
			return
		}
	}
	s.dbg("ensureMainMixLink: rtp-source not found in %d sources", len(snap.Sources))
}

// startLoopback spawns a pw-loopback process for the given speaker.
// Must be called with s.mu held.
func (s *server) startLoopback(mac, nodeName string) {
	cmd := s.spawn(nodeName, s.delays[mac])
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		s.dbg("startLoopback %s: cmd.Start error: %v", mac, err)
		return
	}
	s.dbg("startLoopback %s (%s) pid=%d delay=%dms cmd=%v",
		mac, nodeName, cmd.Process.Pid, s.delays[mac], cmd.Args)
	pid := cmd.Process.Pid
	sp := &speakerState{nodeName: nodeName, cmd: cmd, dead: make(chan struct{}), startedAt: time.Now()}
	s.speakers[mac] = sp
	go func() {
		err := cmd.Wait()
		close(sp.dead)
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		stderrStr := bytes.TrimSpace(stderr.Bytes())
		s.dbg("loopback %s (%s) pid=%d EXITED: err=%q stderr=%q",
			mac, nodeName, pid, errStr, stderrStr)
		s.hub.broadcast(context.Background(), map[string]string{"type": "loopback_stopped", "mac": mac})
	}()
	go s.hub.broadcast(context.Background(), map[string]string{"type": "loopback_started", "mac": mac})
	// Re-apply stored volume and mute after the PW node registers (~1 s).
	// Mute is always applied (defaults false) to override any AVRCP-reported
	// mute state that WirePlumber may set on the PW node when the device connects.
	// Volume and mute are read fresh inside the goroutine (under lock) so that
	// any PUT /volume or PUT /mute arriving during the 1s sleep is not overwritten.
	// A second pass runs 3 s later to catch AVRCP absolute-volume overrides: some
	// BT devices report their hardware volume via AVRCP when audio starts flowing,
	// and WirePlumber applies that to the PW node, undoing our first apply.
	go func() {
		ctx := context.Background()
		for pass, delay := range []time.Duration{time.Second, 3 * time.Second} {
			time.Sleep(delay)
			s.mu.Lock()
			if _, ok := s.speakers[mac]; !ok {
				s.mu.Unlock()
				s.dbg("startLoopback %s: pass %d: speaker removed, stopping vol/mute apply", mac, pass+1)
				return
			}
			// Use the two-value form to distinguish "never set" (skip) from "set to 0"
			// (apply). volume > 0 would silently skip an explicit zero, leaving the
			// speaker at PipeWire's default after reconnect while the UI shows 0.
			volume, hasVolume := s.volumes[mac]
			muted := s.mutes[mac]
			s.mu.Unlock()

			nodeID, err := s.nodeIDForMAC(ctx, mac)
			if err != nil {
				s.dbg("startLoopback %s: pass %d: nodeIDForMAC error: %v", mac, pass+1, err)
				return
			}
			if nodeID < 0 {
				s.dbg("startLoopback %s: pass %d: PW node not found, skipping", mac, pass+1)
				return
			}

			if s.debug {
				if before, gerr := s.audio.GetVolume(ctx, nodeID); gerr == nil {
					s.dbg("startLoopback %s: pass %d: PW vol BEFORE apply: %d (stored=%d node=%d)", mac, pass+1, before, volume, nodeID)
				}
			}
			s.dbg("startLoopback %s: pass %d: applying vol=%d muted=%v to PW nodeID=%d", mac, pass+1, volume, muted, nodeID)
			if hasVolume {
				if verr := s.audio.SetVolume(ctx, nodeID, volume); verr != nil {
					s.dbg("startLoopback %s: pass %d: SetVolume error: %v", mac, pass+1, verr)
				} else if s.debug {
					if after, gerr := s.audio.GetVolume(ctx, nodeID); gerr == nil {
						s.dbg("startLoopback %s: pass %d: PW vol AFTER apply: %d (match=%v)", mac, pass+1, after, after == volume)
					}
				}
			}
			if merr := s.audio.SetMute(ctx, nodeID, muted); merr != nil {
				s.dbg("startLoopback %s: pass %d: SetMute error: %v", mac, pass+1, merr)
			}
		}
	}()
}

// stopLoopback kills the loopback process for the given MAC and removes it from s.speakers.
// Must be called with s.mu held.
func (s *server) stopLoopback(mac string) {
	sp, ok := s.speakers[mac]
	if !ok {
		return
	}
	s.dbg("stopLoopback %s (%s)", mac, sp.nodeName)
	delete(s.speakers, mac)
	if sp.cmd != nil && sp.cmd.Process != nil {
		_ = sp.cmd.Process.Kill()
	}
}

// pauseAllLoopbacks kills every pw-loopback process but leaves the map entries
// intact so tickRouter can revive them when unpaused (speakerDead → startLoopback).
// Must be called with s.mu held.
func (s *server) pauseAllLoopbacks() {
	for _, sp := range s.speakers {
		if sp.cmd != nil && sp.cmd.Process != nil {
			_ = sp.cmd.Process.Kill()
		}
	}
}

func (s *server) killAllLoopbacks() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for mac := range s.speakers {
		s.stopLoopback(mac)
	}
}

func speakerDead(sp *speakerState) bool {
	if sp == nil || sp.dead == nil {
		return true
	}
	select {
	case <-sp.dead:
		return true
	default:
		return false
	}
}
