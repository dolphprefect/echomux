package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"

	"github.com/dolphprefect/echomux/internal/audio"
	"github.com/dolphprefect/echomux/internal/bluetooth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServerInternal(sinks []audio.Node, sources []audio.Node) (*server, *audio.MockController) {
	audioCtr := audio.NewMockController(sinks)
	if sources != nil {
		audioCtr.SetSources(sources)
	}
	btMgr := bluetooth.NewMockManager()
	s := &server{
		bt:               btMgr,
		audio:            audioCtr,
		hub:              newHub(),
		speakers:         make(map[string]*speakerState),
		delays:           make(map[string]int),
		volumes:          make(map[string]int),
		mutes:            make(map[string]bool),
		knownSpeakers:    make(map[string]bool),
		watchdogRestarts: make(map[string]time.Time),
		spawn:            noopSpawn,
	}
	return s, audioCtr
}

// noopSpawn returns a command that succeeds immediately without doing anything.
func noopSpawn(nodeName string, delayMS int) *exec.Cmd {
	return exec.Command("true")
}

func TestTickRouter_LinksRTPToMainMix(t *testing.T) {
	s, audioCtr := newTestServerInternal(nil, []audio.Node{{ID: 99, Name: "rtp-source"}})
	audioCtr.AddNamedNode("main-mix", audio.Node{ID: 200, Name: "main-mix"})

	s.tickRouter(context.Background())

	assert.True(t, audioCtr.Linked(200), "rtp-source should be linked to main-mix")
}

func TestTickRouter_CreatesLoopbackForNewSink(t *testing.T) {
	sinks := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, _ := newTestServerInternal(sinks, nil)

	var spawned []string
	s.spawn = func(nodeName string, _ int) *exec.Cmd {
		spawned = append(spawned, nodeName)
		return exec.Command("true")
	}

	s.tickRouter(context.Background())

	assert.Contains(t, spawned, "bluez_output.AA_BB_CC_DD_EE_FF.1")
	s.mu.Lock()
	_, exists := s.speakers["AA:BB:CC:DD:EE:FF"]
	s.mu.Unlock()
	assert.True(t, exists, "speaker entry should be tracked")
}

func TestTickRouter_KillsLoopbackWhenSinkDisappears(t *testing.T) {
	sinks := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, audioCtr := newTestServerInternal(sinks, nil)
	s.tickRouter(context.Background()) // loopback created

	s.mu.Lock()
	_, exists := s.speakers["AA:BB:CC:DD:EE:FF"]
	s.mu.Unlock()
	require.True(t, exists)

	// Speaker disconnects — no longer returned by Nodes().
	audioCtr.SetSinks(nil)

	s.tickRouter(context.Background()) // should kill the loopback

	s.mu.Lock()
	_, exists = s.speakers["AA:BB:CC:DD:EE:FF"]
	s.mu.Unlock()
	assert.False(t, exists, "loopback should be removed when sink disappears")
}

func TestTickRouter_RestartsDeadLoopback(t *testing.T) {
	sinks := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, _ := newTestServerInternal(sinks, nil)

	spawnCount := 0
	s.spawn = func(nodeName string, _ int) *exec.Cmd {
		spawnCount++
		return exec.Command("true")
	}

	// First tick: loopback created.
	s.tickRouter(context.Background())
	assert.Equal(t, 1, spawnCount)

	// Wait for the "true" command to exit (process dies).
	time.Sleep(50 * time.Millisecond)

	// Second tick: dead loopback restarted.
	s.tickRouter(context.Background())
	assert.Equal(t, 2, spawnCount)
}

func TestTickRouter_DoesNotRespawnLiveLoopback(t *testing.T) {
	sinks := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, _ := newTestServerInternal(sinks, nil)

	spawnCount := 0
	s.spawn = func(nodeName string, _ int) *exec.Cmd {
		spawnCount++
		// Use "sleep" so the process stays alive across ticks.
		return exec.Command("sleep", "10")
	}

	s.tickRouter(context.Background())
	assert.Equal(t, 1, spawnCount)

	s.tickRouter(context.Background())
	assert.Equal(t, 1, spawnCount, "live loopback should not be respawned")

	// Cleanup.
	s.killAllLoopbacks()
}

func TestTickRouter_UsesDelayWhenSpawning(t *testing.T) {
	sinks := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, _ := newTestServerInternal(sinks, nil)
	s.delays["AA:BB:CC:DD:EE:FF"] = 150

	var capturedDelay int
	s.spawn = func(nodeName string, delayMS int) *exec.Cmd {
		capturedDelay = delayMS
		return exec.Command("true")
	}

	s.tickRouter(context.Background())
	assert.Equal(t, 150, capturedDelay)
}

func TestTickRouter_ZombieWatchdog_RestartsOnPausedLink(t *testing.T) {
	const mac = "AA:BB:CC:DD:EE:FF"
	sinks := []audio.Node{{ID: 42, MAC: mac, Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, audioCtr := newTestServerInternal(sinks, nil)

	spawnCount := 0
	s.spawn = func(nodeName string, _ int) *exec.Cmd {
		spawnCount++
		return exec.Command("sleep", "60")
	}

	// First tick: loopback created with active links.
	audioCtr.SetPWLinks([]audio.LinkInfo{
		{ID: 1, OutputNodeID: 10, InputNodeID: 42, State: "active"},
		{ID: 2, OutputNodeID: 10, InputNodeID: 42, State: "active"},
	})
	s.tickRouter(context.Background())
	require.Equal(t, 1, spawnCount)

	// Backdate startedAt to simulate the loopback having run past the grace period.
	s.mu.Lock()
	s.speakers[mac].startedAt = time.Now().Add(-10 * time.Second)
	s.mu.Unlock()

	// Links go paused (zombie state).
	audioCtr.SetPWLinks([]audio.LinkInfo{
		{ID: 1, OutputNodeID: 10, InputNodeID: 42, State: "paused"},
		{ID: 2, OutputNodeID: 10, InputNodeID: 42, State: "paused"},
	})

	s.tickRouter(context.Background())
	assert.Equal(t, 2, spawnCount, "zombie loopback should be restarted when links are paused")

	s.killAllLoopbacks()
}

func TestTickRouter_ZombieWatchdog_RespectsGracePeriod(t *testing.T) {
	const mac = "AA:BB:CC:DD:EE:FF"
	sinks := []audio.Node{{ID: 42, MAC: mac, Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, audioCtr := newTestServerInternal(sinks, nil)

	spawnCount := 0
	s.spawn = func(nodeName string, _ int) *exec.Cmd {
		spawnCount++
		return exec.Command("sleep", "60")
	}

	// Links paused from the start (normal during format negotiation).
	audioCtr.SetPWLinks([]audio.LinkInfo{
		{ID: 1, OutputNodeID: 10, InputNodeID: 42, State: "paused"},
	})

	s.tickRouter(context.Background())
	require.Equal(t, 1, spawnCount)

	// Second tick immediately — still within grace period, must not restart.
	s.tickRouter(context.Background())
	assert.Equal(t, 1, spawnCount, "should not restart loopback within grace period")

	s.killAllLoopbacks()
}

func TestTickRouter_ZombieWatchdog_NoRestartWhenAllActive(t *testing.T) {
	const mac = "AA:BB:CC:DD:EE:FF"
	sinks := []audio.Node{{ID: 42, MAC: mac, Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, audioCtr := newTestServerInternal(sinks, nil)

	spawnCount := 0
	s.spawn = func(nodeName string, _ int) *exec.Cmd {
		spawnCount++
		return exec.Command("sleep", "60")
	}

	audioCtr.SetPWLinks([]audio.LinkInfo{
		{ID: 1, OutputNodeID: 10, InputNodeID: 42, State: "active"},
		{ID: 2, OutputNodeID: 10, InputNodeID: 42, State: "active"},
	})

	s.tickRouter(context.Background())
	require.Equal(t, 1, spawnCount)

	s.mu.Lock()
	s.speakers[mac].startedAt = time.Now().Add(-10 * time.Second)
	s.mu.Unlock()

	s.tickRouter(context.Background())
	assert.Equal(t, 1, spawnCount, "healthy loopback with active links must not be restarted")

	s.killAllLoopbacks()
}

func TestTickRouter_ZombieWatchdog_CooldownPreventsRestartLoop(t *testing.T) {
	const mac = "AA:BB:CC:DD:EE:FF"
	sinks := []audio.Node{{ID: 42, MAC: mac, Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, audioCtr := newTestServerInternal(sinks, nil)

	spawnCount := 0
	s.spawn = func(nodeName string, _ int) *exec.Cmd {
		spawnCount++
		return exec.Command("sleep", "60")
	}

	// Links permanently paused (sink won't recover).
	audioCtr.SetPWLinks([]audio.LinkInfo{
		{ID: 1, OutputNodeID: 10, InputNodeID: 42, State: "paused"},
	})

	s.tickRouter(context.Background())
	require.Equal(t, 1, spawnCount)

	// Backdate startedAt past grace period, and set watchdog cooldown to recent past
	// (but not recent enough to block yet).
	s.mu.Lock()
	s.speakers[mac].startedAt = time.Now().Add(-10 * time.Second)
	s.mu.Unlock()

	// First watchdog trigger: should restart and record the cooldown.
	s.tickRouter(context.Background())
	require.Equal(t, 2, spawnCount)
	s.mu.Lock()
	assert.False(t, s.watchdogRestarts[mac].IsZero(), "watchdogRestarts should be set after watchdog restart")
	s.mu.Unlock()

	// Backdate startedAt again to skip grace period on newly started loopback.
	s.mu.Lock()
	s.speakers[mac].startedAt = time.Now().Add(-10 * time.Second)
	s.mu.Unlock()

	// Second tick immediately: cooldown should suppress another restart.
	s.tickRouter(context.Background())
	assert.Equal(t, 2, spawnCount, "watchdog cooldown should prevent a second restart within 30s")

	s.killAllLoopbacks()
}

func TestTickRouter_PausedDoesNothing(t *testing.T) {
	sinks := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, _ := newTestServerInternal(sinks, nil)
	s.paused = true

	spawnCount := 0
	s.spawn = func(_ string, _ int) *exec.Cmd {
		spawnCount++
		return exec.Command("true")
	}

	s.tickRouter(context.Background())
	assert.Equal(t, 0, spawnCount, "paused tickRouter must not spawn any loopbacks")
}

func TestTickRouter_SnapshotError(t *testing.T) {
	sinks := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, audioCtr := newTestServerInternal(sinks, nil)
	audioCtr.SetSnapshotErr(errors.New("pw-dump failed"))

	spawnCount := 0
	s.spawn = func(_ string, _ int) *exec.Cmd {
		spawnCount++
		return exec.Command("true")
	}

	s.tickRouter(context.Background()) // must not panic
	assert.Equal(t, 0, spawnCount, "snapshot error must not spawn any loopbacks")
}

func TestTickRouter_NodeNameChanged(t *testing.T) {
	const mac = "AA:BB:CC:DD:EE:FF"
	sinks := []audio.Node{{ID: 42, MAC: mac, Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, audioCtr := newTestServerInternal(sinks, nil)

	var spawnedNames []string
	s.spawn = func(nodeName string, _ int) *exec.Cmd {
		spawnedNames = append(spawnedNames, nodeName)
		return exec.Command("sleep", "60")
	}

	// First tick: loopback started with .1 suffix.
	s.tickRouter(context.Background())
	require.Len(t, spawnedNames, 1)
	assert.Equal(t, "bluez_output.AA_BB_CC_DD_EE_FF.1", spawnedNames[0])

	// BT reconnect: PW re-registers the node with a .2 suffix.
	audioCtr.SetSinks([]audio.Node{{ID: 42, MAC: mac, Name: "bluez_output.AA_BB_CC_DD_EE_FF.2"}})

	// Second tick: old loopback killed, new one spawned with updated name.
	s.tickRouter(context.Background())
	require.Len(t, spawnedNames, 2)
	assert.Equal(t, "bluez_output.AA_BB_CC_DD_EE_FF.2", spawnedNames[1])

	s.killAllLoopbacks()
}

func TestHandlePause_KillsLoopbacksButKeepsEntries(t *testing.T) {
	sinks := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, _ := newTestServerInternal(sinks, nil)
	s.spawn = func(_ string, _ int) *exec.Cmd { return exec.Command("sleep", "60") }
	s.tickRouter(context.Background()) // start loopback

	s.mu.Lock()
	sp, exists := s.speakers["AA:BB:CC:DD:EE:FF"]
	s.mu.Unlock()
	require.True(t, exists, "loopback must exist before pause")
	require.False(t, speakerDead(sp), "process must be alive before pause")

	req := httptest.NewRequest(http.MethodPost, "/playback/pause", nil)
	w := httptest.NewRecorder()
	s.handlePause(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)

	s.mu.Lock()
	_, stillExists := s.speakers["AA:BB:CC:DD:EE:FF"]
	paused := s.paused
	s.mu.Unlock()

	assert.True(t, paused, "server must be paused after /playback/pause")
	assert.True(t, stillExists, "map entry must survive pause so tickRouter can revive it on resume")

	// SIGKILL is immediate; give the goroutine a moment to close the dead channel.
	require.Eventually(t, func() bool {
		s.mu.Lock()
		sp2 := s.speakers["AA:BB:CC:DD:EE:FF"]
		s.mu.Unlock()
		return speakerDead(sp2)
	}, 200*time.Millisecond, 10*time.Millisecond, "process should be dead after pause")

	s.killAllLoopbacks()
}

func TestHandleRestart_ClearsWatchdogCooldowns(t *testing.T) {
	sinks := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, _ := newTestServerInternal(sinks, nil)

	// Seed a watchdog cooldown.
	s.mu.Lock()
	s.watchdogRestarts["AA:BB:CC:DD:EE:FF"] = time.Now()
	s.mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/playback/restart", nil)
	w := httptest.NewRecorder()
	s.handleRestart(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	s.mu.Lock()
	_, hasCooldown := s.watchdogRestarts["AA:BB:CC:DD:EE:FF"]
	s.mu.Unlock()
	assert.False(t, hasCooldown, "restart should clear all watchdog cooldowns")
}

// TestStartLoopback_AppliesVolumeZero guards the fix for the "volume=0 not
// re-applied on reconnect" bug. The stored volume=0 must reach SetVolume even
// though the zero value is indistinguishable from "never set" with a plain map
// read — requires the two-value map lookup introduced in the fix.
func TestStartLoopback_AppliesVolumeZero(t *testing.T) {
	const mac = "AA:BB:CC:DD:EE:FF"
	sinks := []audio.Node{{ID: 42, MAC: mac, Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, audioCtr := newTestServerInternal(sinks, nil)

	// Pre-seed a non-zero PW volume so we can detect whether SetVolume(0) was called.
	_ = audioCtr.SetVolume(context.Background(), 42, 50)

	// Explicitly store volume=0 (user silenced this speaker via PUT /volume).
	s.volumes[mac] = 0

	s.spawn = func(_ string, _ int) *exec.Cmd { return exec.Command("sleep", "5") }
	s.tickRouter(context.Background())

	// Wait for the 1 s volume-apply goroutine.
	time.Sleep(1500 * time.Millisecond)

	assert.Equal(t, 0, audioCtr.Volume(42), "volume=0 must be re-applied on loopback restart")
	s.killAllLoopbacks()
}

// TestStartLoopback_SkipsVolumeWhenUnset verifies that the PW node's volume is
// left untouched when no volume has ever been stored server-side.
func TestStartLoopback_SkipsVolumeWhenUnset(t *testing.T) {
	const mac = "AA:BB:CC:DD:EE:FF"
	sinks := []audio.Node{{ID: 42, MAC: mac, Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, audioCtr := newTestServerInternal(sinks, nil)

	// PW node already has a volume; server has no stored volume for this speaker.
	_ = audioCtr.SetVolume(context.Background(), 42, 75)
	// s.volumes[mac] intentionally left unset.

	s.spawn = func(_ string, _ int) *exec.Cmd { return exec.Command("sleep", "5") }
	s.tickRouter(context.Background())

	time.Sleep(1500 * time.Millisecond)

	assert.Equal(t, 75, audioCtr.Volume(42), "PW volume must not be touched when no volume is stored server-side")
	s.killAllLoopbacks()
}

func TestTickRouter_ZombieWatchdog_NoRestartWhenNoLinks(t *testing.T) {
	const mac = "AA:BB:CC:DD:EE:FF"
	sinks := []audio.Node{{ID: 42, MAC: mac, Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	s, _ := newTestServerInternal(sinks, nil)

	spawnCount := 0
	s.spawn = func(nodeName string, _ int) *exec.Cmd {
		spawnCount++
		return exec.Command("sleep", "60")
	}

	// No links at all (Links() returns empty — e.g. pw-dump unavailable).
	s.tickRouter(context.Background())
	require.Equal(t, 1, spawnCount)

	s.mu.Lock()
	s.speakers[mac].startedAt = time.Now().Add(-10 * time.Second)
	s.mu.Unlock()

	s.tickRouter(context.Background())
	assert.Equal(t, 1, spawnCount, "empty link list must not trigger watchdog")

	s.killAllLoopbacks()
}
