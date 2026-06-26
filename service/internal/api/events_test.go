package api_test

import (
	"context"
	"encoding/json"
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

func TestWebSocketEvents(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	btMgr.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: true})
	audioCtr := audio.NewMockController([]audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF"}})

	shutCtx, shutCancel := context.WithCancel(context.Background())
	noop := func(_ string, _ int) *exec.Cmd { return exec.Command("true") }
	srv := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithShutdownContext(shutCtx),
	)
	ts := httptest.NewServer(srv)
	t.Cleanup(func() { shutCancel(); ts.Close() })

	wsURL := "ws" + ts.URL[4:] + "/events"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c1, _, err := websocket.Dial(ctx, wsURL, nil)
	require.NoError(t, err)
	defer c1.CloseNow()

	c2, _, err := websocket.Dial(ctx, wsURL, nil)
	require.NoError(t, err)
	defer c2.CloseNow()

	// trigger a BT event
	time.Sleep(50 * time.Millisecond) // let both clients register
	require.NoError(t, btMgr.Connect(context.Background(), "AA:BB:CC:DD:EE:FF"))

	var ev1, ev2 map[string]string

	require.NoError(t, wsjson.Read(ctx, c1, &ev1))
	require.NoError(t, wsjson.Read(ctx, c2, &ev2))

	for _, ev := range []map[string]string{ev1, ev2} {
		assert.Equal(t, "AA:BB:CC:DD:EE:FF", ev["mac"])
		assert.Equal(t, "connected", ev["type"])
	}

	_ = json.Marshal // suppress unused import if needed
}

// TestEventHub_ClientDisconnectTriggersRemove exercises hub.remove on client disconnect.
// After c1.CloseNow() the server's read pump detects the disconnect and calls hub.remove(c1).
// c2 is then registered and must still receive events normally.
func TestEventHub_ClientDisconnectTriggersRemove(t *testing.T) {
	btMgr := bluetooth.NewMockManager()
	btMgr.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker", Paired: true})
	audioCtr := audio.NewMockController(nil)

	shutCtx, shutCancel := context.WithCancel(context.Background())
	noop := func(_ string, _ int) *exec.Cmd { return exec.Command("true") }
	srv := api.NewServer(btMgr, audioCtr, api.WithSpawn(noop), api.WithShutdownContext(shutCtx))
	ts := httptest.NewServer(srv)
	t.Cleanup(func() { shutCancel(); ts.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	wsURL := "ws" + ts.URL[4:] + "/events"

	// Connect c1, then immediately close it — exercises hub.add then hub.remove.
	c1, _, err := websocket.Dial(ctx, wsURL, nil)
	require.NoError(t, err)
	c1.CloseNow()
	time.Sleep(60 * time.Millisecond) // wait for server goroutine to call hub.remove

	// Connect c2; it should still receive events normally.
	c2, _, err := websocket.Dial(ctx, wsURL, nil)
	require.NoError(t, err)
	defer c2.CloseNow()
	time.Sleep(30 * time.Millisecond) // let c2 register

	require.NoError(t, btMgr.Connect(ctx, "AA:BB:CC:DD:EE:FF"))

	var ev map[string]string
	require.NoError(t, wsjson.Read(ctx, c2, &ev))
	assert.Equal(t, "connected", ev["type"])
	assert.Equal(t, "AA:BB:CC:DD:EE:FF", ev["mac"])
}
