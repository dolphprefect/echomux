package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
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

// masterConn wraps a WS connection from the mock master's perspective.
type masterConn struct {
	conn *websocket.Conn
	t    *testing.T
}

func (mc *masterConn) read() map[string]any {
	mc.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var msg map[string]any
	require.NoError(mc.t, wsjson.Read(ctx, mc.conn, &msg))
	return msg
}

func (mc *masterConn) send(msg any) {
	mc.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(mc.t, wsjson.Write(ctx, mc.conn, msg))
}

// newMockMaster starts a WS server acting as the master. Each incoming satellite
// connection is delivered on the returned channel.
func newMockMaster(t *testing.T) (*httptest.Server, <-chan *masterConn) {
	t.Helper()
	sessions := make(chan *masterConn, 8)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		sessions <- &masterConn{conn: conn, t: t}
		<-r.Context().Done()
		conn.CloseNow()
	}))
	t.Cleanup(ts.Close)
	return ts, sessions
}

// newSatelliteServer creates a satellite-mode server that auto-connects to masterURL.
// The returned cancel stops the satellite client goroutine.
func newSatelliteServer(t *testing.T, masterURL string, btMgr *bluetooth.MockManager) context.CancelFunc {
	t.Helper()
	clientCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	noop := func(_ string, _ int) *exec.Cmd { return exec.Command("true") }

	var macs []string
	devs, _ := btMgr.Devices(context.Background())
	for _, d := range devs {
		macs = append(macs, d.MAC)
	}

	api.NewServer(btMgr, audio.NewMockController(nil),
		api.WithSpawn(noop),
		api.WithMode(api.ModeSatellite),
		api.WithName("Kitchen"),
		api.WithSelfAddr("192.168.1.51:56644"),
		api.WithMasterAddr(masterURL[7:]), // strip "http://"
		api.WithClientContext(clientCtx),
		api.WithKnownSpeakers(macs...),
	)
	return cancel
}

// awaitSession waits up to 3 s for a satellite to connect to the mock master.
func awaitSession(t *testing.T, sessions <-chan *masterConn) *masterConn {
	t.Helper()
	select {
	case mc := <-sessions:
		return mc
	case <-time.After(3 * time.Second):
		t.Fatal("satellite did not connect within 3 s")
		return nil
	}
}

// doHandshake completes register → registered → initial devices handshake.
func doHandshake(t *testing.T, mc *masterConn) {
	t.Helper()
	reg := mc.read()
	require.Equal(t, "register", reg["type"], "expected register message")
	mc.send(map[string]any{"type": "registered", "id": "kitchen"})
	devMsg := mc.read()
	require.Equal(t, "devices", devMsg["type"], "expected initial devices push")
}

// --- Registration ---

func TestSatelliteClientRegisters(t *testing.T) {
	ts, sessions := newMockMaster(t)
	btMgr := bluetooth.NewMockManager()
	newSatelliteServer(t, ts.URL, btMgr)

	mc := awaitSession(t, sessions)
	defer mc.conn.CloseNow()

	reg := mc.read()
	assert.Equal(t, "register", reg["type"])
	assert.Equal(t, "Kitchen", reg["name"])
	assert.Equal(t, "192.168.1.51:56644", reg["addr"])
}

// --- Initial device push ---

func TestSatelliteClientSendsInitialDevices(t *testing.T) {
	ts, sessions := newMockMaster(t)
	btMgr := bluetooth.NewMockManager()
	btMgr.AddDevice(bluetooth.Device{
		MAC: "AA:BB:CC:DD:EE:FF", Name: "Kitchen Speaker", Paired: true,
	})
	newSatelliteServer(t, ts.URL, btMgr)

	mc := awaitSession(t, sessions)
	defer mc.conn.CloseNow()

	mc.read() // register
	mc.send(map[string]any{"type": "registered", "id": "kitchen"})

	devMsg := mc.read()
	assert.Equal(t, "devices", devMsg["type"])
	devs, ok := devMsg["devices"].([]any)
	require.True(t, ok, "devices field must be an array")
	require.Len(t, devs, 1)
	dev := devs[0].(map[string]any)
	assert.Equal(t, "AA:BB:CC:DD:EE:FF", dev["MAC"])
}

func TestSatelliteClientSendsEmptyDevicesWhenNone(t *testing.T) {
	ts, sessions := newMockMaster(t)
	btMgr := bluetooth.NewMockManager()
	newSatelliteServer(t, ts.URL, btMgr)

	mc := awaitSession(t, sessions)
	defer mc.conn.CloseNow()

	mc.read() // register
	mc.send(map[string]any{"type": "registered", "id": "kitchen"})

	devMsg := mc.read()
	assert.Equal(t, "devices", devMsg["type"])
	// Empty or nil devices field — both are acceptable.
	devs, _ := devMsg["devices"].([]any)
	assert.Empty(t, devs)
}

// --- Ping / pong ---

func TestSatelliteClientRespondsToPing(t *testing.T) {
	ts, sessions := newMockMaster(t)
	btMgr := bluetooth.NewMockManager()
	newSatelliteServer(t, ts.URL, btMgr)

	mc := awaitSession(t, sessions)
	defer mc.conn.CloseNow()
	doHandshake(t, mc)

	mc.send(map[string]any{"type": "ping"})
	pong := mc.read()
	assert.Equal(t, "pong", pong["type"])
}

// --- BT event forwarding ---

func TestSatelliteClientForwardsBTEvent(t *testing.T) {
	ts, sessions := newMockMaster(t)
	btMgr := bluetooth.NewMockManager()
	btMgr.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker"})
	newSatelliteServer(t, ts.URL, btMgr)

	mc := awaitSession(t, sessions)
	defer mc.conn.CloseNow()
	doHandshake(t, mc)

	require.NoError(t, btMgr.Connect(context.Background(), "AA:BB:CC:DD:EE:FF"))

	ev := mc.read()
	assert.Equal(t, "event", ev["type"])
	assert.Equal(t, "AA:BB:CC:DD:EE:FF", ev["mac"])
	assert.Equal(t, "connected", ev["event"])
}

func TestSatelliteClientBTEventsSeqIncrements(t *testing.T) {
	ts, sessions := newMockMaster(t)
	btMgr := bluetooth.NewMockManager()
	btMgr.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker"})
	newSatelliteServer(t, ts.URL, btMgr)

	mc := awaitSession(t, sessions)
	defer mc.conn.CloseNow()
	doHandshake(t, mc)

	require.NoError(t, btMgr.Connect(context.Background(), "AA:BB:CC:DD:EE:FF"))
	ev1 := mc.read()
	assert.Equal(t, "event", ev1["type"])
	// seq=0 may be omitted by omitempty — presence not required for first event.

	require.NoError(t, btMgr.Disconnect(context.Background(), "AA:BB:CC:DD:EE:FF"))
	ev2 := mc.read()
	assert.Equal(t, "event", ev2["type"])
	assert.Equal(t, float64(1), ev2["seq"], "seq must increment to 1 on second event")
}

// --- request_sync ---

func TestSatelliteClientRequestSync(t *testing.T) {
	ts, sessions := newMockMaster(t)
	btMgr := bluetooth.NewMockManager()
	btMgr.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker", Paired: true})
	newSatelliteServer(t, ts.URL, btMgr)

	mc := awaitSession(t, sessions)
	defer mc.conn.CloseNow()
	doHandshake(t, mc)

	mc.send(map[string]any{"type": "request_sync"})
	syncMsg := mc.read()
	assert.Equal(t, "devices", syncMsg["type"])
	devs, ok := syncMsg["devices"].([]any)
	require.True(t, ok)
	require.Len(t, devs, 1)
}

// --- Reconnect ---

func TestSatelliteClientReconnectsAfterDisconnect(t *testing.T) {
	ts, sessions := newMockMaster(t)
	btMgr := bluetooth.NewMockManager()
	newSatelliteServer(t, ts.URL, btMgr)

	// First session: register, then disconnect.
	mc1 := awaitSession(t, sessions)
	mc1.read() // consume register so the handler doesn't block
	mc1.conn.CloseNow()

	// Satellite must reconnect within 5 s (initial 1 s backoff ± 15% jitter).
	mc2 := awaitSession(t, sessions)
	defer mc2.conn.CloseNow()

	reg := mc2.read()
	assert.Equal(t, "register", reg["type"])
}

// TestSatelliteClientBackoffResetsAfterSuccess verifies the attempt counter
// resets after a successful session so reconnects after brief glitches are fast.
func TestSatelliteClientBackoffResetsAfterSuccess(t *testing.T) {
	ts, sessions := newMockMaster(t)
	btMgr := bluetooth.NewMockManager()
	newSatelliteServer(t, ts.URL, btMgr)

	for i := 0; i < 2; i++ {
		mc := awaitSession(t, sessions)
		mc.read() // register
		mc.send(map[string]any{"type": "registered", "id": "kitchen"})
		mc.read() // devices
		mc.conn.CloseNow()
	}
	// Third reconnect should still arrive within 3 s (reset to ~1 s, not 4 s).
	mc3 := awaitSession(t, sessions)
	defer mc3.conn.CloseNow()
	reg := mc3.read()
	assert.Equal(t, "register", reg["type"])
}

func TestSatelliteClientPushesDevicesOnStateChange(t *testing.T) {
	ts, sessions := newMockMaster(t)
	btMgr := bluetooth.NewMockManager()
	mac := "AA:BB:CC:DD:EE:FF"
	btMgr.AddDevice(bluetooth.Device{MAC: mac, Name: "Speaker", Paired: true})

	clientCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	noop := func(_ string, _ int) *exec.Cmd { return exec.Command("true") }

	mockAudio := audio.NewMockController(nil)
	mockAudio.SetSinks([]audio.Node{
		{ID: 12, MAC: mac, Name: "bluez_output.AA_BB_CC_DD_EE_FF.1"},
	})

	srv := api.NewServer(btMgr, mockAudio,
		api.WithSpawn(noop),
		api.WithMode(api.ModeSatellite),
		api.WithName("Kitchen"),
		api.WithSelfAddr("192.168.1.51:56644"),
		api.WithMasterAddr(ts.URL[7:]), // strip "http://"
		api.WithClientContext(clientCtx),
		api.WithKnownSpeakers(mac),
	)

	mc := awaitSession(t, sessions)
	defer mc.conn.CloseNow()
	doHandshake(t, mc)

	satTS := httptest.NewServer(srv)
	defer satTS.Close()

	reqCtx, reqCancel := context.WithTimeout(context.Background(), time.Second)
	defer reqCancel()
	req, err := http.NewRequestWithContext(reqCtx, "PUT", satTS.URL+"/devices/"+mac+"/volume", strings.NewReader(`{"level":85}`))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	devMsg := mc.read()
	assert.Equal(t, "devices", devMsg["type"])
	devs, ok := devMsg["devices"].([]any)
	require.True(t, ok)
	require.Len(t, devs, 1)
	dev := devs[0].(map[string]any)
	assert.Equal(t, float64(85), dev["volume"])
}
