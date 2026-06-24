package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/dolphprefect/echomux/internal/api"
	"github.com/dolphprefect/echomux/internal/audio"
	"github.com/dolphprefect/echomux/internal/bluetooth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServer(t *testing.T) (*httptest.Server, *bluetooth.MockManager, *audio.MockController) {
	t.Helper()
	btMgr := bluetooth.NewMockManager()
	btMgr.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: true})
	btMgr.AddDevice(bluetooth.Device{MAC: "11:22:33:44:55:66", Name: "Speaker B", Paired: true})

	nodes := []audio.Node{
		{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"},
		{ID: 77, MAC: "11:22:33:44:55:66", Name: "bluez_output.11_22_33_44_55_66.1"},
	}
	audioCtr := audio.NewMockController(nodes) // includes default rtp-source ID=99

	noop := func(nodeName string, _ int) *exec.Cmd { return exec.Command("true") }
	srv := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithKnownSpeakers("AA:BB:CC:DD:EE:FF", "11:22:33:44:55:66"),
	)
	return httptest.NewServer(srv), btMgr, audioCtr
}

func TestGetDevices(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var devs []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&devs))
	assert.Len(t, devs, 2)
}

func TestGetDevices_IncludesDelayMs(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	// Default delay should be 0.
	resp, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp.Body.Close()

	var devs []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&devs))
	for _, d := range devs {
		assert.Equal(t, float64(0), d["delay_ms"], "default delay should be 0")
	}
}

func TestPostScan(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]int{"timeout_sec": 1})
	resp, err := http.Post(ts.URL+"/scan", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var devs []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&devs))
	assert.NotEmpty(t, devs)
}

func TestPostConnect(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/devices/AA:BB:CC:DD:EE:FF/connect", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestPostConnectUnknownMAC(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/devices/00:00:00:00:00:00/connect", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPostDisconnect(t *testing.T) {
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	require.NoError(t, btMgr.Connect(context.Background(), "AA:BB:CC:DD:EE:FF"))

	resp, err := http.Post(ts.URL+"/devices/AA:BB:CC:DD:EE:FF/disconnect", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestPostPair(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/devices/AA:BB:CC:DD:EE:FF/pair", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestPostPairUnknownMAC(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/devices/00:00:00:00:00:00/pair", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPutVolume(t *testing.T) {
	ts, _, audioCtr := newTestServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]int{"level": 80})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/volume", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, 80, audioCtr.Volume(42))
}

func TestPutVolumeOutOfRange(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]int{"level": 150})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/volume", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPutMute(t *testing.T) {
	ts, _, audioCtr := newTestServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]bool{"muted": true})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/mute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.True(t, audioCtr.Muted(42))
}

func TestPutDelay(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]int{"ms": 120})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/delay", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Delay is persisted in the service and reflected in GET /devices.
	resp2, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp2.Body.Close()
	var devs []map[string]any
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&devs))
	for _, d := range devs {
		if d["MAC"] == "AA:BB:CC:DD:EE:FF" {
			assert.Equal(t, float64(120), d["delay_ms"])
			return
		}
	}
	t.Fatal("device AA:BB:CC:DD:EE:FF not found in GET /devices response")
}

func TestDeleteDevice_Forget(t *testing.T) {
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/devices/AA:BB:CC:DD:EE:FF", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Device removed from BlueZ mock.
	devs, _ := btMgr.Devices(context.Background())
	for _, d := range devs {
		assert.NotEqual(t, "AA:BB:CC:DD:EE:FF", d.MAC, "device should be removed from BlueZ")
	}

	// Device gone from /devices (no longer a known speaker).
	resp2, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp2.Body.Close()
	var list []map[string]any
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&list))
	for _, d := range list {
		assert.NotEqual(t, "AA:BB:CC:DD:EE:FF", d["MAC"], "device should be gone from /devices")
	}
}

func TestGetInput(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/input")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var sources []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&sources))
	require.Len(t, sources, 1)
	assert.Equal(t, "rtp-source", sources[0]["Name"])
}

func TestPostInputDiscover(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"enabled": true, "timeout_sec": 30})
	resp, err := http.Post(ts.URL+"/input/discover", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestGetStream(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/stream")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, true, body["active"])
	assert.Equal(t, float64(99), body["source_node_id"])
}

func TestGetStream_NoSources(t *testing.T) {
	ts, _, audioCtr := newTestServer(t)
	defer ts.Close()

	audioCtr.SetSources(nil)

	resp, err := http.Get(ts.URL + "/stream")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, false, body["active"])
}

func TestPostPause(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/playback/pause", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestPostResume(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	// Pause first, then resume.
	resp, err := http.Post(ts.URL+"/playback/pause", "application/json", nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	resp, err = http.Post(ts.URL+"/playback/resume", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestPostRestart(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/playback/restart", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestPutDelayOutOfRange(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	for _, ms := range []int{-1, 2001} {
		body, _ := json.Marshal(map[string]int{"ms": ms})
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/delay", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "ms=%d should be rejected", ms)
	}
}

func TestPutVolumeNodeNotFound(t *testing.T) {
	ts, _, audioCtr := newTestServer(t)
	defer ts.Close()

	// Remove all PW sink nodes so nodeIDForMAC returns -1.
	audioCtr.SetSinks(nil)

	body, _ := json.Marshal(map[string]int{"level": 50})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/volume", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPutMuteNodeNotFound(t *testing.T) {
	ts, _, audioCtr := newTestServer(t)
	defer ts.Close()

	audioCtr.SetSinks(nil)

	body, _ := json.Marshal(map[string]bool{"muted": true})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/mute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPostDisconnectUnknownMAC(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/devices/00:00:00:00:00:00/disconnect", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestGetDevices_PendingVolumeResolution(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	btMgr.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: true})

	nodes := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	audioCtr := audio.NewMockController(nodes)
	// Inject error to GetVolume() call to simulate unavailable volume.
	audioCtr.SetGetVolumeErr(errors.New("PipeWire volume query failed"))

	noop := func(nodeName string, _ int) *exec.Cmd { return exec.Command("true") }
	srv := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithKnownSpeakers("AA:BB:CC:DD:EE:FF"),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// MockController.GetVolume returns an error, so handleGetDevices should
	// mark volume as -1.
	resp, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp.Body.Close()
	var devs []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&devs))
	require.Len(t, devs, 1)
	assert.Equal(t, float64(-1), devs[0]["volume"])
}

func TestGetDevices_VolumeZeroResolution(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	btMgr.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: true})

	nodes := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	audioCtr := audio.NewMockController(nodes)
	// Explicitly set volume to 0 so MockController returns 0.
	audioCtr.SetVolume(context.Background(), 42, 0) //nolint

	noop := func(nodeName string, _ int) *exec.Cmd { return exec.Command("true") }
	srv := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithKnownSpeakers("AA:BB:CC:DD:EE:FF"),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// GetVolume returns 0, so handleGetDevices should resolve the volume to 0.
	resp, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp.Body.Close()
	var devs []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&devs))
	require.Len(t, devs, 1)
	assert.Equal(t, float64(0), devs[0]["volume"])
}

func TestGetDevices_NodesErrorOnPendingVolume(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	btMgr.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: true})

	// No sinks initially so the pending-volume Nodes() call is exercised.
	audioCtr := audio.NewMockController(nil)
	audioCtr.SetNodesErr(errors.New("pw-dump failed"))

	noop := func(nodeName string, _ int) *exec.Cmd { return exec.Command("true") }
	srv := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithKnownSpeakers("AA:BB:CC:DD:EE:FF"),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Nodes() error must not crash the handler or change the status code;
	// device is returned with volume=-1 (PW node unavailable).
	resp, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var devs []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&devs))
	require.Len(t, devs, 1)
	assert.Equal(t, float64(-1), devs[0]["volume"])
}

func TestGetDevices_DevicesError(t *testing.T) {
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	btMgr.SetDevicesErr(errors.New("bluez crashed"))

	resp, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestPostScan_DevicesErrAfterScan(t *testing.T) {
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	btMgr.SetDevicesErr(errors.New("bluez crashed"))

	body, _ := json.Marshal(map[string]int{"timeout_sec": 1})
	resp, err := http.Post(ts.URL+"/scan", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// TestPostConnect_TransientErrRetries verifies that a transient Connect error
// triggers a retry and the request ultimately succeeds (~2s due to retry delay).
func TestPostConnect_TransientErrRetries(t *testing.T) {
	t.Parallel()
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	btMgr.SetConnectErr(errors.New("page timeout"), 1)

	resp, err := http.Post(ts.URL+"/devices/AA:BB:CC:DD:EE:FF/connect", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// TestPostConnect_AllRetriesExhausted verifies that exhausting all retries
// returns 500 (~4s due to two retry delays).
func TestPostConnect_AllRetriesExhausted(t *testing.T) {
	t.Parallel()
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	btMgr.SetConnectErr(errors.New("page timeout"), 999)

	resp, err := http.Post(ts.URL+"/devices/AA:BB:CC:DD:EE:FF/connect", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestPostPair_RescanPath(t *testing.T) {
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	// Device IS in the mock so the second Pair (after re-scan) will succeed.
	// The first Pair is forced to return DeviceNotFoundError to trigger the
	// re-scan path in handlePair.
	btMgr.SetPairErr(&bluetooth.DeviceNotFoundError{MAC: "AA:BB:CC:DD:EE:FF"}, 1)

	resp, err := http.Post(ts.URL+"/devices/AA:BB:CC:DD:EE:FF/pair", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestDeleteDevice_ForgetGenericError(t *testing.T) {
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	btMgr.SetForgetErr(errors.New("dbus error"))

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/devices/AA:BB:CC:DD:EE:FF", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestGetInput_SourcesError(t *testing.T) {
	ts, _, audioCtr := newTestServer(t)
	defer ts.Close()

	audioCtr.SetSourcesErr(errors.New("pw-dump failed"))

	resp, err := http.Get(ts.URL + "/input")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestPostInputDiscover_Error(t *testing.T) {
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	btMgr.SetDiscoverableErr(errors.New("dbus error"))

	body, _ := json.Marshal(map[string]any{"enabled": true, "timeout_sec": 30})
	resp, err := http.Post(ts.URL+"/input/discover", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestPostDisconnect_GenericError(t *testing.T) {
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	btMgr.SetDisconnectErr(errors.New("dbus error"))

	resp, err := http.Post(ts.URL+"/devices/AA:BB:CC:DD:EE:FF/disconnect", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestPostPair_AlreadyExists(t *testing.T) {
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	// BlueZ returns "Already Exists" when a device is already paired; must be treated as success.
	btMgr.SetPairErr(errors.New("org.bluez.Error.AlreadyExists: Already Exists"), 999)

	resp, err := http.Post(ts.URL+"/devices/AA:BB:CC:DD:EE:FF/pair", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestDeleteDevice_ForgetNotFoundTreatedAsSuccess(t *testing.T) {
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	// Forget returns DeviceNotFoundError (device already gone from BlueZ, e.g. factory reset).
	btMgr.SetForgetErr(&bluetooth.DeviceNotFoundError{MAC: "AA:BB:CC:DD:EE:FF"})

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/devices/AA:BB:CC:DD:EE:FF", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestPutVolume_InvalidJSON(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/volume", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPutVolume_SetVolumeError(t *testing.T) {
	ts, _, audioCtr := newTestServer(t)
	defer ts.Close()

	audioCtr.SetVolumeErr(errors.New("pipewire error"))

	body, _ := json.Marshal(map[string]int{"level": 50})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/volume", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestPutMute_InvalidJSON(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/mute", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPutMute_SetMuteError(t *testing.T) {
	ts, _, audioCtr := newTestServer(t)
	defer ts.Close()

	audioCtr.SetMuteErr(errors.New("pipewire error"))

	body, _ := json.Marshal(map[string]bool{"muted": true})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/mute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestPutDelay_InvalidJSON(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/delay", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestGetStream_SourcesError(t *testing.T) {
	ts, _, audioCtr := newTestServer(t)
	defer ts.Close()

	audioCtr.SetSourcesErr(errors.New("pw-dump failed"))

	resp, err := http.Get(ts.URL + "/stream")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestPostInputDiscover_DefaultBody(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	// nil body and timeout_sec=0 should both fall back to the defaults (enabled=true, 60s).
	for _, body := range []string{"", `{"timeout_sec":0}`} {
		resp, err := http.Post(ts.URL+"/input/discover", "application/json", bytes.NewBufferString(body))
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusNoContent, resp.StatusCode, "body=%q", body)
	}
}

func TestGetDevices_ExcludesNonKnownSpeakers(t *testing.T) {
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	// Add a BT device that is NOT a known speaker (e.g. a phone or keyboard).
	btMgr.AddDevice(bluetooth.Device{MAC: "FF:EE:DD:CC:BB:AA", Name: "Phone"})

	resp, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp.Body.Close()

	var devs []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&devs))
	assert.Len(t, devs, 2, "non-known-speaker BT devices must be excluded from /devices")
	for _, d := range devs {
		assert.NotEqual(t, "FF:EE:DD:CC:BB:AA", d["MAC"])
	}
}

func TestPostScan_ScanError(t *testing.T) {
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	btMgr.SetScanErr(errors.New("hci0 down"))

	body, _ := json.Marshal(map[string]int{"timeout_sec": 1})
	resp, err := http.Post(ts.URL+"/scan", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestPostScan_DefaultTimeout(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	// Empty body and timeout_sec=0 should both fall back to 10s (mock scan returns immediately).
	for _, body := range []string{"", `{"timeout_sec":0}`} {
		resp, err := http.Post(ts.URL+"/scan", "application/json", bytes.NewBufferString(body))
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, "body=%q", body)
	}
}

func TestPostConnect_AlreadyConnected(t *testing.T) {
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	// BlueZ returns "AlreadyConnected" when the device is already up; treat as success.
	btMgr.SetConnectErr(errors.New("AlreadyConnected: Already connected"), 999)

	resp, err := http.Post(ts.URL+"/devices/AA:BB:CC:DD:EE:FF/connect", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestPutVolume_NodesError(t *testing.T) {
	ts, _, audioCtr := newTestServer(t)
	defer ts.Close()

	audioCtr.SetNodesErr(errors.New("pw-dump failed"))

	body, _ := json.Marshal(map[string]int{"level": 50})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/volume", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestPutMute_NodesError(t *testing.T) {
	ts, _, audioCtr := newTestServer(t)
	defer ts.Close()

	audioCtr.SetNodesErr(errors.New("pw-dump failed"))

	body, _ := json.Marshal(map[string]bool{"muted": true})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/mute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestGetDevices_EmptyKnownSpeakers(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)
	noop := func(nodeName string, _ int) *exec.Cmd { return exec.Command("true") }
	srv := api.NewServer(btMgr, audioCtr, api.WithSpawn(noop))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var devs []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&devs))
	assert.Empty(t, devs, "no known speakers → empty JSON array, not null")
}

func TestGetDevices_PendingVolumeFromPipeWire(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	btMgr.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: true})
	nodes := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	audioCtr := audio.NewMockController(nodes)
	// No volume stored in server state → resolves via GetVolume.
	// MockController.GetVolume returns 100 by default (no explicit volume set).

	noop := func(nodeName string, _ int) *exec.Cmd { return exec.Command("true") }
	srv := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithKnownSpeakers("AA:BB:CC:DD:EE:FF"),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp.Body.Close()
	var devs []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&devs))
	require.Len(t, devs, 1)
	assert.Equal(t, float64(100), devs[0]["volume"], "volume resolved from PipeWire when not stored server-side")
}

func TestPutVolume_ValidBoundary(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	for _, level := range []int{0, 100} {
		body, _ := json.Marshal(map[string]int{"level": level})
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/volume", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusNoContent, resp.StatusCode, "level=%d must be accepted", level)
	}
}

func TestPutDelay_ValidBoundary(t *testing.T) {
	ts, _, _ := newTestServer(t)
	defer ts.Close()

	for _, ms := range []int{0, 2000} {
		body, _ := json.Marshal(map[string]int{"ms": ms})
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/devices/AA:BB:CC:DD:EE:FF/delay", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusNoContent, resp.StatusCode, "ms=%d must be accepted", ms)
	}
}

func TestGetDevices_PlayingField(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	btMgr.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: true})
	nodes := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	audioCtr := audio.NewMockController(nodes)

	// Long-lived process so playing=true persists long enough to be checked.
	longLived := func(_ string, _ int) *exec.Cmd { return exec.Command("sleep", "5") }
	srv := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(longLived),
		api.WithKnownSpeakers("AA:BB:CC:DD:EE:FF"),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Pause then immediately resume: resume calls tickRouter synchronously,
	// which starts the loopback before the 2-second background tick fires.
	http.Post(ts.URL+"/playback/pause", "application/json", nil)   //nolint
	http.Post(ts.URL+"/playback/resume", "application/json", nil)  //nolint

	resp, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp.Body.Close()
	var devs []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&devs))
	require.Len(t, devs, 1)
	assert.Equal(t, true, devs[0]["playing"], "playing must be true while the loopback process is alive")
}

func TestPostPair_BothAttemptsNotFound(t *testing.T) {
	ts, btMgr, _ := newTestServer(t)
	defer ts.Close()

	// Both the initial pair call and the retry after re-scan fail with DeviceNotFoundError.
	btMgr.SetPairErr(&bluetooth.DeviceNotFoundError{MAC: "AA:BB:CC:DD:EE:FF"}, 2)

	resp, err := http.Post(ts.URL+"/devices/AA:BB:CC:DD:EE:FF/pair", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestGetInput_EmptySources(t *testing.T) {
	ts, _, audioCtr := newTestServer(t)
	defer ts.Close()

	audioCtr.SetSources(nil)

	resp, err := http.Get(ts.URL + "/input")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var sources []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&sources))
	assert.Empty(t, sources, "empty sources must return an empty JSON array, not null")
}

func TestStateFilePersistence(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")

	btMgr := bluetooth.NewMockManager()
	btMgr.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: true})
	nodes := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"}}
	audioCtr := audio.NewMockController(nodes)
	noop := func(nodeName string, _ int) *exec.Cmd { return exec.Command("true") }

	// Server 1: set a delay, then close.
	srv1 := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithKnownSpeakers("AA:BB:CC:DD:EE:FF"),
		api.WithStateFile(stateFile),
	)
	ts1 := httptest.NewServer(srv1)

	body, _ := json.Marshal(map[string]int{"ms": 250})
	req, _ := http.NewRequest(http.MethodPut, ts1.URL+"/devices/AA:BB:CC:DD:EE:FF/delay", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	ts1.Close()

	// saveState runs in a goroutine; wait up to 500 ms for the file to appear.
	require.Eventually(t, func() bool {
		_, err := os.Stat(stateFile)
		return err == nil
	}, 500*time.Millisecond, 10*time.Millisecond, "state file should be written after delay update")

	// Server 2: load from state file, delay should be restored.
	srv2 := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithKnownSpeakers("AA:BB:CC:DD:EE:FF"),
		api.WithStateFile(stateFile),
	)
	ts2 := httptest.NewServer(srv2)
	defer ts2.Close()

	resp2, err := http.Get(ts2.URL + "/devices")
	require.NoError(t, err)
	defer resp2.Body.Close()
	var devs []map[string]any
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&devs))
	require.Len(t, devs, 1)
	assert.Equal(t, float64(250), devs[0]["delay_ms"], "delay should be restored from state file")
}
