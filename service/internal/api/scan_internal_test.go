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

// TestHandleScan_ScanError_UnpausesAndReturnsEmpty verifies that when Scan()
// fails the server unpauses and the response is 200 with an empty array.
// POST /scan flushes 200 before the scan starts (so reverse proxies with short
// ResponseHeaderTimeout don't time out), which means errors cannot change the
// status code — they are signalled by returning [] instead of a device list.
func TestHandleScan_ScanError_UnpausesAndReturnsEmpty(t *testing.T) {
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

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `[]`, w.Body.String(), "error response body must be empty array")
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

// TestHandleScan_DevicesError_UnpausesAndReturnsEmpty verifies that when
// Devices() fails after a successful scan the server unpauses and returns 200
// with an empty array (same early-flush contract as the scan-error case).
func TestHandleScan_DevicesError_UnpausesAndReturnsEmpty(t *testing.T) {
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

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `[]`, w.Body.String(), "error response body must be empty array")
	s.mu.Lock()
	paused := s.paused
	s.mu.Unlock()
	assert.False(t, paused, "server must unpause when Devices() fails after scan")
}
