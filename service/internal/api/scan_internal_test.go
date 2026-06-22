package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dolphprefect/echomux/internal/audio"
	"github.com/dolphprefect/echomux/internal/bluetooth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleScan_UnpausesOnScanError verifies that s.paused is reset to false
// when Scan() itself returns an error (not just the subsequent Devices() call).
func TestHandleScan_UnpausesOnScanError(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	btMgr.SetScanErr(errors.New("hci0 down"))

	audioCtr := audio.NewMockController(nil)
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

	body, _ := json.Marshal(map[string]int{"timeout_sec": 1})
	req := httptest.NewRequest(http.MethodPost, "/scan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleScan(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	s.mu.Lock()
	paused := s.paused
	s.mu.Unlock()
	assert.False(t, paused, "server must unpause when Scan() itself fails")
}

// TestHandleScan_StaysPausedAfterSuccess verifies that s.paused remains true
// after a successful scan. The client (not the server) is responsible for
// calling POST /playback/resume once the scan sheet closes, so the server must
// not unpause itself on a clean scan result.
func TestHandleScan_StaysPausedAfterSuccess(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	btMgr.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A"})

	audioCtr := audio.NewMockController(nil)
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

	body, _ := json.Marshal(map[string]int{"timeout_sec": 1})
	req := httptest.NewRequest(http.MethodPost, "/scan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleScan(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	s.mu.Lock()
	paused := s.paused
	s.mu.Unlock()
	assert.True(t, paused, "server must remain paused after a successful scan")
}

// TestHandleScan_UnpausesOnDevicesError verifies that s.paused is reset to
// false when Devices() fails after a successful scan. Without the fix, the
// server would be stuck paused indefinitely.
func TestHandleScan_UnpausesOnDevicesError(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	btMgr.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A"})
	btMgr.SetDevicesErr(errors.New("bluez crashed"))

	audioCtr := audio.NewMockController(nil)
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

	body, _ := json.Marshal(map[string]int{"timeout_sec": 1})
	req := httptest.NewRequest(http.MethodPost, "/scan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleScan(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	s.mu.Lock()
	paused := s.paused
	s.mu.Unlock()
	assert.False(t, paused, "server must unpause when Devices() fails after scan")
}
