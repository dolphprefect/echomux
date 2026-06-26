package bluetooth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dolphprefect/echomux/internal/bluetooth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockManager_Devices(t *testing.T) {
	m := bluetooth.NewMockManager()
	m.AddDevice(bluetooth.Device{
		MAC:    "AA:BB:CC:DD:EE:FF",
		Name:   "Speaker A",
		Paired: true,
	})

	devs, err := m.Devices(context.Background())
	require.NoError(t, err)
	require.Len(t, devs, 1)
	assert.Equal(t, "AA:BB:CC:DD:EE:FF", devs[0].MAC)
	assert.Equal(t, "Speaker A", devs[0].Name)
	assert.True(t, devs[0].Paired)
	assert.False(t, devs[0].Connected)
}

func TestMockManager_Connect(t *testing.T) {
	m := bluetooth.NewMockManager()
	m.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: true})

	ctx := context.Background()
	err := m.Connect(ctx, "AA:BB:CC:DD:EE:FF")
	require.NoError(t, err)

	// device shows connected
	devs, err := m.Devices(ctx)
	require.NoError(t, err)
	assert.True(t, devs[0].Connected)

	// event emitted
	select {
	case ev := <-m.Events():
		assert.Equal(t, "AA:BB:CC:DD:EE:FF", ev.MAC)
		assert.Equal(t, "connected", ev.Type)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("no event received")
	}
}

func TestMockManager_Disconnect(t *testing.T) {
	m := bluetooth.NewMockManager()
	m.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: true})

	ctx := context.Background()
	require.NoError(t, m.Connect(ctx, "AA:BB:CC:DD:EE:FF"))
	<-m.Events() // drain connect event

	require.NoError(t, m.Disconnect(ctx, "AA:BB:CC:DD:EE:FF"))

	devs, err := m.Devices(ctx)
	require.NoError(t, err)
	assert.False(t, devs[0].Connected)

	select {
	case ev := <-m.Events():
		assert.Equal(t, "disconnected", ev.Type)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("no event received")
	}
}

func TestMockManager_ConnectUnknownMAC(t *testing.T) {
	m := bluetooth.NewMockManager()
	err := m.Connect(context.Background(), "00:00:00:00:00:00")
	require.Error(t, err)
	var nf *bluetooth.DeviceNotFoundError
	assert.ErrorAs(t, err, &nf)
	assert.Equal(t, "00:00:00:00:00:00", nf.MAC)
}

func TestMockManager_Scan(t *testing.T) {
	m := bluetooth.NewMockManager()
	err := m.Scan(context.Background(), 100*time.Millisecond)
	assert.NoError(t, err)
}

func TestMockManager_Pair(t *testing.T) {
	m := bluetooth.NewMockManager()
	m.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: false})

	ctx := context.Background()
	err := m.Pair(ctx, "AA:BB:CC:DD:EE:FF")
	require.NoError(t, err)

	devs, err := m.Devices(ctx)
	require.NoError(t, err)
	assert.True(t, devs[0].Paired)

	select {
	case ev := <-m.Events():
		assert.Equal(t, "AA:BB:CC:DD:EE:FF", ev.MAC)
		assert.Equal(t, "paired", ev.Type)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("no event received")
	}
}

func TestMockManager_PairUnknownMAC(t *testing.T) {
	m := bluetooth.NewMockManager()
	err := m.Pair(context.Background(), "00:00:00:00:00:00")
	require.Error(t, err)
	var nf *bluetooth.DeviceNotFoundError
	assert.ErrorAs(t, err, &nf)
}

func TestMockManager_SetDiscoverable(t *testing.T) {
	m := bluetooth.NewMockManager()
	err := m.SetDiscoverable(context.Background(), true, 60*time.Second)
	assert.NoError(t, err)
}

func TestMockManager_SetConnectErr_RetryCount(t *testing.T) {
	m := bluetooth.NewMockManager()
	m.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: true})
	ctx := context.Background()

	// Set error for 2 attempts, then succeed.
	m.SetConnectErr(errors.New("page timeout"), 2)

	// First call: returns error.
	err := m.Connect(ctx, "AA:BB:CC:DD:EE:FF")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "page timeout")

	// Second call: returns error (failN=1 left).
	err = m.Connect(ctx, "AA:BB:CC:DD:EE:FF")
	require.Error(t, err)

	// Third call: succeds (failN exhausted).
	err = m.Connect(ctx, "AA:BB:CC:DD:EE:FF")
	require.NoError(t, err)
}

func TestMockManager_SetPairErr_RetryCount(t *testing.T) {
	m := bluetooth.NewMockManager()
	m.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: false})
	ctx := context.Background()

	m.SetPairErr(&bluetooth.DeviceNotFoundError{MAC: "AA:BB:CC:DD:EE:FF"}, 1)

	// First call: returns the DeviceNotFoundError.
	err := m.Pair(ctx, "AA:BB:CC:DD:EE:FF")
	var nf *bluetooth.DeviceNotFoundError
	assert.ErrorAs(t, err, &nf)

	// Second call: succeeds (failN exhausted, device found in map).
	err = m.Pair(ctx, "AA:BB:CC:DD:EE:FF")
	require.NoError(t, err)

	// Should be paired now.
	devs, _ := m.Devices(ctx)
	assert.True(t, devs[0].Paired)
}

func TestMockManager_SetForgetErr(t *testing.T) {
	m := bluetooth.NewMockManager()
	m.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: true})
	ctx := context.Background()

	m.SetForgetErr(errors.New("dbus error"))

	err := m.Forget(ctx, "AA:BB:CC:DD:EE:FF")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dbus error")

	// Device should still be in the map.
	devs, _ := m.Devices(ctx)
	assert.Len(t, devs, 1)
}

func TestMockManager_Forget_RemovesDevice(t *testing.T) {
	m := bluetooth.NewMockManager()
	m.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: true})

	require.NoError(t, m.Forget(context.Background(), "AA:BB:CC:DD:EE:FF"))

	devs, _ := m.Devices(context.Background())
	assert.Len(t, devs, 0)
}

func TestMockManager_Forget_UnknownMAC_ReturnsDeviceNotFound(t *testing.T) {
	m := bluetooth.NewMockManager()
	err := m.Forget(context.Background(), "00:00:00:00:00:00")
	var nf *bluetooth.DeviceNotFoundError
	assert.ErrorAs(t, err, &nf)
}

func TestMockManager_SetDevicesErr(t *testing.T) {
	m := bluetooth.NewMockManager()
	m.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A"})

	m.SetDevicesErr(errors.New("bluez crashed"))

	_, err := m.Devices(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bluez crashed")

	// After error, subsequent calls still fail (persistent until cleared).
	_, err = m.Devices(context.Background())
	require.Error(t, err)
}

func TestMockManager_SetDiscoverableErr(t *testing.T) {
	m := bluetooth.NewMockManager()
	m.SetDiscoverableErr(errors.New("dbus error"))

	err := m.SetDiscoverable(context.Background(), true, 30*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dbus error")
}

func TestMockManager_EventChannel_Backpressure(t *testing.T) {
	m := bluetooth.NewMockManager()
	m.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: true})

	ctx := context.Background()
	// MockManager event channel has capacity 16. Connect 20 times in a row
	// to verify that non-blocking sends work (drops) rather than blocking.
	for i := 0; i < 20; i++ {
		err := m.Connect(ctx, "AA:BB:CC:DD:EE:FF")
		// AlreadyConnected-like "already" message after first connect
		// in real BlueZ, but mock always sets Connected=true. No error.
		require.NoError(t, err)
	}
	// Drain events — should get at least 16 without blocking.
	count := 0
	drainLoop:
	for {
		select {
		case <-m.Events():
			count++
		case <-time.After(50 * time.Millisecond):
			break drainLoop
		}
	}
	assert.GreaterOrEqual(t, count, 16, "should have received at least 16 events")
}

func TestMockManager_Disconnect_ReturnsDeviceNotFound(t *testing.T) {
	m := bluetooth.NewMockManager()
	err := m.Disconnect(context.Background(), "00:00:00:00:00:00")
	var nf *bluetooth.DeviceNotFoundError
	assert.ErrorAs(t, err, &nf)
}

func TestMockManager_SetDisconnectErr(t *testing.T) {
	m := bluetooth.NewMockManager()
	m.AddDevice(bluetooth.Device{MAC: "AA:BB:CC:DD:EE:FF", Name: "Speaker A", Paired: true})
	m.SetDisconnectErr(errors.New("dbus timeout"))

	err := m.Disconnect(context.Background(), "AA:BB:CC:DD:EE:FF")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dbus timeout")

	// Even a missing MAC goes through the error path first.
	err = m.Disconnect(context.Background(), "00:00:00:00:00:00")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dbus timeout")
}

func TestMockManager_SetScanErr(t *testing.T) {
	m := bluetooth.NewMockManager()
	m.SetScanErr(errors.New("hci0 down"))

	err := m.Scan(context.Background(), 100*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hci0 down")
}
