package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"

	"github.com/dolphprefect/echomux/internal/api"
	"github.com/dolphprefect/echomux/internal/audio"
	"github.com/dolphprefect/echomux/internal/bluetooth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestProxy_HappyPath(t *testing.T) {
	// 1. Start a mock satellite server.
	satMux := http.NewServeMux()
	satMux.HandleFunc("GET /devices", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"MAC":"AA:BB","Name":"Speaker"}]`))
	})
	satSrv := httptest.NewServer(satMux)
	defer satSrv.Close()

	// 2. Start the Master server.
	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)
	noop := func(_ string, _ int) *exec.Cmd { return exec.Command("true") }
	master := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithMode(api.ModeMaster),
		api.WithName("Master"),
	)
	masterSrv := httptest.NewServer(master)
	defer masterSrv.Close()

	// Register the satellite node.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+masterSrv.URL[4:]+"/nodes", nil)
	require.NoError(t, err)
	defer conn.CloseNow()

	satHost := satSrv.URL[7:] // strip "http://"

	registerMsg := map[string]any{
		"type": "register",
		"name": "Kitchen",
		"addr": satHost,
	}
	require.NoError(t, wsjson.Write(ctx, conn, registerMsg))

	var ack map[string]any
	require.NoError(t, wsjson.Read(ctx, conn, &ack))
	require.Equal(t, "registered", ack["type"])

	time.Sleep(20 * time.Millisecond)

	// 3. Make HTTP request to Master to proxy: GET /nodes/kitchen/devices
	resp, err := http.Get(masterSrv.URL + "/nodes/kitchen/devices")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"MAC":"AA:BB","Name":"Speaker"}]`, string(body))
}

func TestProxy_NodeNotFound(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)
	noop := func(_ string, _ int) *exec.Cmd { return exec.Command("true") }
	master := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithMode(api.ModeMaster),
		api.WithName("Master"),
	)
	masterSrv := httptest.NewServer(master)
	defer masterSrv.Close()

	resp, err := http.Get(masterSrv.URL + "/nodes/nonexistent/devices")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	var errBody map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))
	assert.Equal(t, "node_not_found", errBody["error"])
	assert.Equal(t, "nonexistent", errBody["node"])
}

func TestProxy_NodeOffline(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)
	noop := func(_ string, _ int) *exec.Cmd { return exec.Command("true") }
	master := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithMode(api.ModeMaster),
		api.WithName("Master"),
	)
	masterSrv := httptest.NewServer(master)
	defer masterSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+masterSrv.URL[4:]+"/nodes", nil)
	require.NoError(t, err)

	registerMsg := map[string]any{
		"type": "register",
		"name": "Kitchen",
		"addr": "192.168.1.51:56644",
	}
	require.NoError(t, wsjson.Write(ctx, conn, registerMsg))
	var ack map[string]any
	require.NoError(t, wsjson.Read(ctx, conn, &ack))

	// Close connection to mark it offline.
	conn.CloseNow()
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(masterSrv.URL + "/nodes/kitchen/devices")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	var errBody map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))
	assert.Equal(t, "node_offline", errBody["error"])
	assert.Equal(t, "kitchen", errBody["node"])
}

func TestProxy_Satellite5xxMappedTo504(t *testing.T) {
	satMux := http.NewServeMux()
	satMux.HandleFunc("GET /devices", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("satellite blew up"))
	})
	satSrv := httptest.NewServer(satMux)
	defer satSrv.Close()

	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)
	noop := func(_ string, _ int) *exec.Cmd { return exec.Command("true") }
	master := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithMode(api.ModeMaster),
		api.WithName("Master"),
	)
	masterSrv := httptest.NewServer(master)
	defer masterSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+masterSrv.URL[4:]+"/nodes", nil)
	require.NoError(t, err)
	defer conn.CloseNow()

	registerMsg := map[string]any{
		"type": "register",
		"name": "Kitchen",
		"addr": satSrv.URL[7:],
	}
	require.NoError(t, wsjson.Write(ctx, conn, registerMsg))
	var ack map[string]any
	require.NoError(t, wsjson.Read(ctx, conn, &ack))
	time.Sleep(20 * time.Millisecond)

	resp, err := http.Get(masterSrv.URL + "/nodes/kitchen/devices")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusGatewayTimeout, resp.StatusCode)
	var errBody map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))
	assert.Equal(t, "bluetooth_subsystem", errBody["error"])
	assert.Equal(t, "kitchen", errBody["node"])
}

func TestProxy_UnreachableSatelliteMappedTo504(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)
	noop := func(_ string, _ int) *exec.Cmd { return exec.Command("true") }
	master := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithMode(api.ModeMaster),
		api.WithName("Master"),
	)
	masterSrv := httptest.NewServer(master)
	defer masterSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+masterSrv.URL[4:]+"/nodes", nil)
	require.NoError(t, err)
	defer conn.CloseNow()

	// Register with an unreachable IP port.
	registerMsg := map[string]any{
		"type": "register",
		"name": "Kitchen",
		"addr": "192.0.2.1:56644", // TEST-NET-1 unreachable address.
	}
	require.NoError(t, wsjson.Write(ctx, conn, registerMsg))
	var ack map[string]any
	require.NoError(t, wsjson.Read(ctx, conn, &ack))
	time.Sleep(20 * time.Millisecond)

	resp, err := http.Get(masterSrv.URL + "/nodes/kitchen/devices")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusGatewayTimeout, resp.StatusCode)
	var errBody map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))
	assert.Equal(t, "bluetooth_subsystem", errBody["error"])
	assert.Equal(t, "kitchen", errBody["node"])
}

func TestProxy_URLEncodedPath(t *testing.T) {
	// 1. Start a mock satellite server.
	satMux := http.NewServeMux()
	var receivedPath string
	satMux.HandleFunc("PUT /devices/{mac}/volume", func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	satSrv := httptest.NewServer(satMux)
	defer satSrv.Close()

	// 2. Start the Master server.
	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)
	noop := func(_ string, _ int) *exec.Cmd { return exec.Command("true") }
	master := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithMode(api.ModeMaster),
		api.WithName("Master"),
	)
	masterSrv := httptest.NewServer(master)
	defer masterSrv.Close()

	// Register the satellite node.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+masterSrv.URL[4:]+"/nodes", nil)
	require.NoError(t, err)
	defer conn.CloseNow()

	satHost := satSrv.URL[7:] // strip "http://"

	registerMsg := map[string]any{
		"type": "register",
		"name": "Kitchen",
		"addr": satHost,
	}
	require.NoError(t, wsjson.Write(ctx, conn, registerMsg))

	var ack map[string]any
	require.NoError(t, wsjson.Read(ctx, conn, &ack))
	require.Equal(t, "registered", ack["type"])

	time.Sleep(20 * time.Millisecond)

	// 3. Make HTTP request to Master to proxy: PUT /nodes/kitchen/devices/AA%3ABB/volume
	req, err := http.NewRequest("PUT", masterSrv.URL+"/nodes/kitchen/devices/AA%3ABB/volume", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, "/devices/AA:BB/volume", receivedPath)
}

func TestProxy_PreservesURLEncoding(t *testing.T) {
	// 1. Start a mock satellite server.
	satMux := http.NewServeMux()
	var receivedURI string
	satMux.HandleFunc("GET /devices/{mac}", func(w http.ResponseWriter, r *http.Request) {
		receivedURI = r.RequestURI
		w.WriteHeader(http.StatusOK)
	})
	satSrv := httptest.NewServer(satMux)
	defer satSrv.Close()

	// 2. Start the Master server.
	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)
	noop := func(_ string, _ int) *exec.Cmd { return exec.Command("true") }
	master := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithMode(api.ModeMaster),
		api.WithName("Master"),
	)
	masterSrv := httptest.NewServer(master)
	defer masterSrv.Close()

	// Register the satellite node.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+masterSrv.URL[4:]+"/nodes", nil)
	require.NoError(t, err)
	defer conn.CloseNow()

	satHost := satSrv.URL[7:] // strip "http://"

	registerMsg := map[string]any{
		"type": "register",
		"name": "Kitchen",
		"addr": satHost,
	}
	require.NoError(t, wsjson.Write(ctx, conn, registerMsg))

	var ack map[string]any
	require.NoError(t, wsjson.Read(ctx, conn, &ack))
	require.Equal(t, "registered", ack["type"])

	time.Sleep(20 * time.Millisecond)

	// 3. Make HTTP request to Master to proxy: GET /nodes/kitchen/devices/foo%2Fbar
	req, err := http.NewRequest("GET", masterSrv.URL+"/nodes/kitchen/devices/foo%2Fbar", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// We expect the URL-encoded path segment to be preserved as foo%2Fbar, not decoded to foo/bar.
	assert.Equal(t, "/devices/foo%2Fbar", receivedURI)
}

func TestProxy_StandaloneModeForbidden(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)
	noop := func(_ string, _ int) *exec.Cmd { return exec.Command("true") }
	srv := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithMode(api.ModeStandalone),
		api.WithName("Standalone"),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/nodes/kitchen/devices")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	var errBody map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))
	assert.Equal(t, "only_master_mode_supports_proxy", errBody["error"])
}



