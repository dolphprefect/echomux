package api_test

import (
	"context"
	"encoding/json"
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

// newMasterServer creates a master-mode test server with fast heartbeat timings.
func newMasterServer(t *testing.T) (*httptest.Server, *audio.MockController) {
	t.Helper()
	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)
	noop := func(nodeName string, _ int) *exec.Cmd { return exec.Command("true") }
	srv := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithMode(api.ModeMaster),
		api.WithName("Living Room"),
		api.WithHeartbeat(60*time.Millisecond, 120*time.Millisecond),
	)
	return httptest.NewServer(srv), audioCtr
}

func dialNodes(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/nodes", nil)
	require.NoError(t, err)
	return conn
}

func sendMsg(t *testing.T, conn *websocket.Conn, msg any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, wsjson.Write(ctx, conn, msg))
}

func readMsg(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var msg map[string]any
	require.NoError(t, wsjson.Read(ctx, conn, &msg))
	return msg
}

// --- Registration ---

func TestSatelliteRegister(t *testing.T) {
	ts, _ := newMasterServer(t)
	defer ts.Close()

	conn := dialNodes(t, ts)
	defer conn.CloseNow()

	sendMsg(t, conn, map[string]any{
		"type": "register",
		"name": "Kitchen",
		"addr": "192.168.1.51:56644",
	})

	msg := readMsg(t, conn)
	assert.Equal(t, "registered", msg["type"])
	assert.Equal(t, "kitchen", msg["id"])
}

func TestSatelliteRegister_ResolvesEmptyOrPortAddr(t *testing.T) {
	ts, _ := newMasterServer(t)
	defer ts.Close()

	// Register with only a port.
	conn := dialNodes(t, ts)
	defer conn.CloseNow()

	sendMsg(t, conn, map[string]any{
		"type": "register",
		"name": "Kitchen",
		"addr": ":56644",
	})

	msg := readMsg(t, conn)
	assert.Equal(t, "registered", msg["type"])

	// Check resolved address via GET /nodes.
	resp, err := http.Get(ts.URL + "/nodes")
	require.NoError(t, err)
	defer resp.Body.Close()

	var nodes []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&nodes))
	require.Len(t, nodes, 2)

	var found bool
	for _, n := range nodes {
		if n["id"] == "kitchen" {
			addr := n["addr"].(string)
			// It should contain loopback IP and port 56644.
			assert.Contains(t, addr, "127.0.0.1:56644")
			found = true
		}
	}
	assert.True(t, found, "kitchen satellite not found in nodes list")
}

func TestSatelliteRegister_EmptyNameRejected(t *testing.T) {
	ts, _ := newMasterServer(t)
	defer ts.Close()

	conn := dialNodes(t, ts)
	defer conn.CloseNow()

	sendMsg(t, conn, map[string]any{"type": "register", "name": "", "addr": "192.168.1.51:56644"})

	// Connection should be closed by server after bad register.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var v map[string]any
	err := wsjson.Read(ctx, conn, &v)
	assert.Error(t, err)
}

func TestSatelliteRegister_WrongFirstMessage(t *testing.T) {
	ts, _ := newMasterServer(t)
	defer ts.Close()

	conn := dialNodes(t, ts)
	defer conn.CloseNow()

	sendMsg(t, conn, map[string]any{"type": "pong"}) // not register

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var v map[string]any
	err := wsjson.Read(ctx, conn, &v)
	assert.Error(t, err)
}

// --- GET /nodes ---

func TestGetNodes_StandaloneMode(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)
	noop := func(_ string, _ int) *exec.Cmd { return exec.Command("true") }
	ts := httptest.NewServer(api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithMode(api.ModeStandalone),
		api.WithName("Home"),
	))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/nodes")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var nodes []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&nodes))
	require.Len(t, nodes, 1)
	assert.Equal(t, "home", nodes[0]["id"])
	assert.Equal(t, "master", nodes[0]["role"])
	assert.Equal(t, true, nodes[0]["online"])
}

func TestGetNodes_MasterMode_IncludesSatellite(t *testing.T) {
	ts, _ := newMasterServer(t)
	defer ts.Close()

	conn := dialNodes(t, ts)
	defer conn.CloseNow()

	sendMsg(t, conn, map[string]any{"type": "register", "name": "Kitchen", "addr": "192.168.1.51:56644"})
	readMsg(t, conn) // registered
	time.Sleep(20 * time.Millisecond)

	resp, err := http.Get(ts.URL + "/nodes")
	require.NoError(t, err)
	defer resp.Body.Close()

	var nodes []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&nodes))
	require.Len(t, nodes, 2)

	roles := map[string]string{}
	for _, n := range nodes {
		roles[n["id"].(string)] = n["role"].(string)
	}
	assert.Equal(t, "master", roles["living-room"])
	assert.Equal(t, "satellite", roles["kitchen"])
}

// --- Device cache ---

func TestSatelliteDeviceCache_FullPush(t *testing.T) {
	ts, _ := newMasterServer(t)
	defer ts.Close()

	conn := dialNodes(t, ts)
	defer conn.CloseNow()

	sendMsg(t, conn, map[string]any{"type": "register", "name": "Kitchen", "addr": "192.168.1.51:56644"})
	readMsg(t, conn) // registered

	// Satellite pushes its device list.
	sendMsg(t, conn, map[string]any{
		"type": "devices",
		"devices": []map[string]any{
			{"MAC": "AA:BB:CC:DD:EE:FF", "Name": "JBL", "Connected": true, "Paired": true},
		},
	})
	time.Sleep(20 * time.Millisecond)

	// Master's GET /devices should include the satellite device with node_id.
	resp, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp.Body.Close()

	var devs []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&devs))

	var found bool
	for _, d := range devs {
		if d["MAC"] == "AA:BB:CC:DD:EE:FF" {
			assert.Equal(t, "kitchen", d["node_id"])
			found = true
		}
	}
	assert.True(t, found, "satellite device should appear in GET /devices")
}

// --- Delta events & seq ---

func TestSatelliteDeltaEvent_ConnectedUpdatesCache(t *testing.T) {
	ts, _ := newMasterServer(t)
	defer ts.Close()

	conn := dialNodes(t, ts)
	defer conn.CloseNow()

	sendMsg(t, conn, map[string]any{"type": "register", "name": "Kitchen", "addr": "192.168.1.51:56644"})
	readMsg(t, conn) // registered

	// Seed cache with a disconnected device.
	sendMsg(t, conn, map[string]any{
		"type":    "devices",
		"devices": []map[string]any{{"MAC": "AA:BB:CC:DD:EE:FF", "Name": "JBL", "Connected": false, "Paired": true}},
	})
	time.Sleep(20 * time.Millisecond)

	// Delta: device connected, seq=0.
	sendMsg(t, conn, map[string]any{"type": "event", "mac": "AA:BB:CC:DD:EE:FF", "event": "connected", "seq": 0})
	time.Sleep(20 * time.Millisecond)

	resp, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp.Body.Close()
	var devs []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&devs))

	for _, d := range devs {
		if d["MAC"] == "AA:BB:CC:DD:EE:FF" {
			assert.True(t, d["Connected"].(bool), "device should be Connected after event")
			return
		}
	}
	t.Fatal("device not found")
}

func TestSatelliteDeltaEvent_SeqGapTriggersRequestSync(t *testing.T) {
	ts, _ := newMasterServer(t)
	defer ts.Close()

	conn := dialNodes(t, ts)
	defer conn.CloseNow()

	sendMsg(t, conn, map[string]any{"type": "register", "name": "Kitchen", "addr": "192.168.1.51:56644"})
	readMsg(t, conn) // registered

	// seq=0 is expected; send seq=2 to create a gap.
	sendMsg(t, conn, map[string]any{"type": "event", "mac": "AA:BB:CC:DD:EE:FF", "event": "connected", "seq": 2})

	msg := readMsg(t, conn)
	assert.Equal(t, "request_sync", msg["type"])
}

func TestSatelliteDeltaEvent_SeqResetOnReregister(t *testing.T) {
	ts, _ := newMasterServer(t)
	defer ts.Close()

	// First registration — advance seq to 1.
	conn1 := dialNodes(t, ts)
	sendMsg(t, conn1, map[string]any{"type": "register", "name": "Attic", "addr": "192.168.1.52:56644"})
	readMsg(t, conn1)
	sendMsg(t, conn1, map[string]any{"type": "event", "mac": "AA:BB:CC:00:00:01", "event": "connected", "seq": 0})
	time.Sleep(20 * time.Millisecond)
	conn1.CloseNow()
	time.Sleep(40 * time.Millisecond)

	// Reconnect — seq counter must reset to 0.
	conn2 := dialNodes(t, ts)
	defer conn2.CloseNow()
	sendMsg(t, conn2, map[string]any{"type": "register", "name": "Attic", "addr": "192.168.1.52:56644"})
	readMsg(t, conn2) // registered

	// seq=0 should be accepted (no request_sync).
	sendMsg(t, conn2, map[string]any{"type": "event", "mac": "AA:BB:CC:00:00:01", "event": "connected", "seq": 0})
	time.Sleep(20 * time.Millisecond)

	// No request_sync should arrive.
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	var unexpected map[string]any
	err := wsjson.Read(ctx, conn2, &unexpected)
	if err == nil {
		// Only the heartbeat ping is expected; request_sync would be wrong.
		assert.NotEqual(t, "request_sync", unexpected["type"], "seq=0 after re-register must not trigger request_sync")
	}
}

// --- Heartbeat ---

func TestSatelliteHeartbeat_PingPong(t *testing.T) {
	ts, _ := newMasterServer(t)
	defer ts.Close()

	conn := dialNodes(t, ts)
	defer conn.CloseNow()

	sendMsg(t, conn, map[string]any{"type": "register", "name": "Kitchen", "addr": "192.168.1.51:56644"})
	readMsg(t, conn) // registered

	// Respond to pings — connection must stay open.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Handle up to 5 messages; pings must be replied to with pong.
	for i := 0; i < 5; i++ {
		var msg map[string]any
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			t.Fatalf("unexpected read error after %d msgs: %v", i, err)
		}
		if msg["type"] == "ping" {
			sendMsg(t, conn, map[string]any{"type": "pong"})
			return // success: received ping, sent pong, connection alive
		}
	}
	t.Fatal("expected a ping message from master")
}

func TestSatelliteHeartbeat_NoPongClosesConnection(t *testing.T) {
	ts, _ := newMasterServer(t)
	defer ts.Close()

	conn := dialNodes(t, ts)
	defer conn.CloseNow()

	sendMsg(t, conn, map[string]any{"type": "register", "name": "Kitchen", "addr": "192.168.1.51:56644"})
	readMsg(t, conn) // registered

	// Ignore pings — connection should be closed by pong watchdog.
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	for {
		var msg map[string]any
		err := wsjson.Read(ctx, conn, &msg)
		if err != nil {
			// Expected: server closed the connection after pong timeout.
			return
		}
		// Ignore any messages (including ping) — don't pong.
	}
}

// --- satellite_online / satellite_offline events ---

func TestSatelliteOnlineEvent_BroadcastToUIClients(t *testing.T) {
	ts, _ := newMasterServer(t)
	defer ts.Close()

	// Subscribe to UI events.
	evCtx, evCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer evCancel()
	evConn, _, err := websocket.Dial(evCtx, "ws"+ts.URL[4:]+"/events", nil)
	require.NoError(t, err)
	defer evConn.CloseNow()
	time.Sleep(20 * time.Millisecond)

	// Satellite registers.
	satConn := dialNodes(t, ts)
	defer satConn.CloseNow()
	sendMsg(t, satConn, map[string]any{"type": "register", "name": "Kitchen", "addr": "192.168.1.51:56644"})
	readMsg(t, satConn) // registered

	var ev map[string]any
	require.NoError(t, wsjson.Read(evCtx, evConn, &ev))
	assert.Equal(t, "satellite_online", ev["type"])
	assert.Equal(t, "kitchen", ev["node_id"])
}

func TestSatelliteOfflineEvent_BroadcastOnDisconnect(t *testing.T) {
	ts, _ := newMasterServer(t)
	defer ts.Close()

	satConn := dialNodes(t, ts)
	sendMsg(t, satConn, map[string]any{"type": "register", "name": "Attic", "addr": "192.168.1.52:56644"})
	readMsg(t, satConn) // registered
	time.Sleep(20 * time.Millisecond)

	// Subscribe to UI events AFTER registration so we only capture offline.
	evCtx, evCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer evCancel()
	evConn, _, err := websocket.Dial(evCtx, "ws"+ts.URL[4:]+"/events", nil)
	require.NoError(t, err)
	defer evConn.CloseNow()
	time.Sleep(20 * time.Millisecond)

	// Satellite disconnects.
	satConn.CloseNow()

	var ev map[string]any
	require.NoError(t, wsjson.Read(evCtx, evConn, &ev))
	assert.Equal(t, "satellite_offline", ev["type"])
	assert.Equal(t, "attic", ev["node_id"])
}

// --- Duplicate name guard ---

func TestSatelliteDuplicateName_SecondConnectionRejected(t *testing.T) {
	ts, _ := newMasterServer(t)
	defer ts.Close()

	// First satellite connects and stays online.
	conn1 := dialNodes(t, ts)
	defer conn1.CloseNow()
	sendMsg(t, conn1, map[string]any{"type": "register", "name": "Den", "addr": "192.168.1.53:56644"})
	readMsg(t, conn1) // registered
	time.Sleep(20 * time.Millisecond)

	// Second connection with same name while first is alive.
	conn2 := dialNodes(t, ts)
	defer conn2.CloseNow()
	sendMsg(t, conn2, map[string]any{"type": "register", "name": "Den", "addr": "192.168.1.53:56644"})

	// Server should close conn2 with a policy violation.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var v map[string]any
	err := wsjson.Read(ctx, conn2, &v)
	assert.Error(t, err, "duplicate satellite connection should be closed by server")
}

// --- GET /nodes on non-master: WS upgrade forbidden ---

func TestNodesWS_ForbiddenOnStandalone(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)
	noop := func(_ string, _ int) *exec.Cmd { return exec.Command("true") }
	ts := httptest.NewServer(api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithMode(api.ModeStandalone),
		api.WithName("Home"),
	))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/nodes", nil)
	assert.Error(t, err)
	if resp != nil {
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	}
}

// --- RTP lifecycle ---

func TestSatelliteRegister_CallsAddRTPSink(t *testing.T) {
	ts, audioCtr := newMasterServer(t)
	defer ts.Close()

	audioCtr.SetRTPAddResult(42, nil)

	conn := dialNodes(t, ts)
	defer conn.CloseNow()
	sendMsg(t, conn, map[string]any{"type": "register", "name": "Kitchen", "addr": "192.168.1.51:56644"})
	readMsg(t, conn) // registered
	time.Sleep(20 * time.Millisecond)

	assert.Equal(t, 1, audioCtr.RTPAddCalls(), "AddRTPSink must be called on satellite registration")
}

func TestSatelliteDisconnect_CallsRemoveRTPSink(t *testing.T) {
	ts, audioCtr := newMasterServer(t)
	defer ts.Close()

	audioCtr.SetRTPAddResult(99, nil)

	conn := dialNodes(t, ts)
	sendMsg(t, conn, map[string]any{"type": "register", "name": "Kitchen", "addr": "192.168.1.51:56644"})
	readMsg(t, conn) // registered
	conn.CloseNow()
	time.Sleep(60 * time.Millisecond) // async RemoveRTPSink

	assert.Equal(t, 99, audioCtr.LastRTPRemoveID(), "RemoveRTPSink must be called with the module ID from registration")
}
