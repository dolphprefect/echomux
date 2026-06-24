package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dolphprefect/echomux/internal/audio"
	"github.com/dolphprefect/echomux/internal/bluetooth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDevices_MasterModeCacheRead(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)

	// Add local device.
	btMgr.AddDevice(bluetooth.Device{MAC: "11:22:33:44:55:66", Name: "Local Speaker", Connected: true, Paired: true})

	s := &server{
		bt:                btMgr,
		audio:             audioCtr,
		hub:               newHub(),
		speakers:          make(map[string]*speakerState),
		delays:            make(map[string]int),
		volumes:           make(map[string]int),
		mutes:             make(map[string]bool),
		knownSpeakers:     map[string]bool{"11:22:33:44:55:66": true},
		watchdogRestarts:  make(map[string]time.Time),
		spawn:             noopSpawn,
		mode:              ModeMaster,
		name:              "Living Room",
		nodes:             newNodeRegistry(),
		heartbeatInterval: 10 * time.Second,
		pongTimeout:       5 * time.Second,
	}

	// Directly populate cache in node registry.
	s.nodes.commit("kitchen", "Kitchen", "192.168.1.51:56644", 0, nil)
	s.nodes.setDevices("kitchen", []deviceInfo{
		{
			Device: bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Satellite Speaker", Connected: true, Paired: true},
			Volume: 80,
			Muted:  false,
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/devices", nil)
	w := httptest.NewRecorder()

	s.handleGetDevices(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var devs []deviceInfo
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &devs))

	require.Len(t, devs, 2)

	// Local device stamped with master's slugified node_id ("living-room").
	assert.Equal(t, "11:22:33:44:55:66", devs[0].MAC)
	assert.Equal(t, "living-room", devs[0].NodeID)

	// Satellite device stamped with registry n.ID ("kitchen").
	assert.Equal(t, "AA:BB:CC:DD:EE:FF", devs[1].MAC)
	assert.Equal(t, "kitchen", devs[1].NodeID)
	assert.Equal(t, 80, devs[1].Volume)
}

func TestGetDevices_StandaloneModeNoNodeID(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)

	btMgr.AddDevice(bluetooth.Device{MAC: "11:22:33:44:55:66", Name: "Local Speaker", Connected: true, Paired: true})

	s := &server{
		bt:                btMgr,
		audio:             audioCtr,
		hub:               newHub(),
		speakers:          make(map[string]*speakerState),
		delays:            make(map[string]int),
		volumes:           make(map[string]int),
		mutes:             make(map[string]bool),
		knownSpeakers:     map[string]bool{"11:22:33:44:55:66": true},
		watchdogRestarts:  make(map[string]time.Time),
		spawn:             noopSpawn,
		mode:              ModeStandalone,
		name:              "Living Room",
		nodes:             newNodeRegistry(),
		heartbeatInterval: 10 * time.Second,
		pongTimeout:       5 * time.Second,
	}

	req := httptest.NewRequest(http.MethodGet, "/devices", nil)
	w := httptest.NewRecorder()

	s.handleGetDevices(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var devs []deviceInfo
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &devs))

	require.Len(t, devs, 1)
	assert.Equal(t, "11:22:33:44:55:66", devs[0].MAC)
	assert.Empty(t, devs[0].NodeID)
}

func TestGetDevices_MasterModeSkipsOfflineNodes(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)

	btMgr.AddDevice(bluetooth.Device{MAC: "11:22:33:44:55:66", Name: "Local Speaker", Connected: true, Paired: true})

	s := &server{
		bt:                btMgr,
		audio:             audioCtr,
		hub:               newHub(),
		speakers:          make(map[string]*speakerState),
		delays:            make(map[string]int),
		volumes:           make(map[string]int),
		mutes:             make(map[string]bool),
		knownSpeakers:     map[string]bool{"11:22:33:44:55:66": true},
		watchdogRestarts:  make(map[string]time.Time),
		spawn:             noopSpawn,
		mode:              ModeMaster,
		name:              "Living Room",
		nodes:             newNodeRegistry(),
		heartbeatInterval: 10 * time.Second,
		pongTimeout:       5 * time.Second,
	}

	// Directly populate cache in node registry.
	s.nodes.commit("kitchen", "Kitchen", "192.168.1.51:56644", 0, nil)
	s.nodes.setDevices("kitchen", []deviceInfo{
		{
			Device: bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Satellite Speaker A", Connected: true, Paired: true},
			Volume: 80,
			Muted:  false,
		},
	})

	// Offline node:
	s.nodes.commit("patio", "Patio", "192.168.1.52:56644", 0, nil)
	s.nodes.setDevices("patio", []deviceInfo{
		{
			Device: bluetooth.Device{MAC: "22:33:44:55:66:77", Name: "Satellite Speaker B", Connected: true, Paired: true},
			Volume: 90,
			Muted:  false,
		},
	})
	s.nodes.setOffline("patio") // make patio offline

	req := httptest.NewRequest(http.MethodGet, "/devices", nil)
	w := httptest.NewRecorder()

	s.handleGetDevices(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var devs []deviceInfo
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &devs))

	// Should only contain Local Speaker + Kitchen Speaker (Patio is offline so skipped).
	require.Len(t, devs, 2)
	assert.Equal(t, "11:22:33:44:55:66", devs[0].MAC)
	assert.Equal(t, "AA:BB:CC:DD:EE:FF", devs[1].MAC)
}
