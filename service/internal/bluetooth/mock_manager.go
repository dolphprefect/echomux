package bluetooth

import (
	"context"
	"sync"
	"time"
)

type MockManager struct {
	mu      sync.Mutex
	devices map[string]Device
	events  chan Event

	devicesErr    error
	connectErr    error
	connectFailN  int
	disconnectErr error
	forgetErr     error
	discoverErr   error
	pairErr       error
	pairFailN     int
	scanErr       error
}

func NewMockManager() *MockManager {
	return &MockManager{
		devices: make(map[string]Device),
		events:  make(chan Event, 16),
	}
}

func (m *MockManager) AddDevice(d Device) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.devices[d.MAC] = d
}

// SetDevicesErr causes all subsequent Devices() calls to return err.
func (m *MockManager) SetDevicesErr(err error) {
	m.mu.Lock()
	m.devicesErr = err
	m.mu.Unlock()
}

// SetConnectErr causes the next failN Connect() calls to return err instead of
// the normal lookup. After failN calls the mock resumes normal behaviour.
func (m *MockManager) SetConnectErr(err error, failN int) {
	m.mu.Lock()
	m.connectErr = err
	m.connectFailN = failN
	m.mu.Unlock()
}

// SetDisconnectErr causes Disconnect() to return err regardless of whether the
// device exists. Use a non-DeviceNotFoundError to exercise the 500 path.
func (m *MockManager) SetDisconnectErr(err error) {
	m.mu.Lock()
	m.disconnectErr = err
	m.mu.Unlock()
}

// SetForgetErr causes Forget() to return err regardless of whether the device
// exists. Use a non-DeviceNotFoundError to exercise the 500 path.
func (m *MockManager) SetForgetErr(err error) {
	m.mu.Lock()
	m.forgetErr = err
	m.mu.Unlock()
}

// SetDiscoverableErr causes SetDiscoverable() to return err.
func (m *MockManager) SetDiscoverableErr(err error) {
	m.mu.Lock()
	m.discoverErr = err
	m.mu.Unlock()
}

// SetPairErr causes the next failN Pair() calls to return err. After failN
// calls the mock resumes normal behaviour (device map lookup).
func (m *MockManager) SetPairErr(err error, failN int) {
	m.mu.Lock()
	m.pairErr = err
	m.pairFailN = failN
	m.mu.Unlock()
}

// SetScanErr causes Scan() to return err.
func (m *MockManager) SetScanErr(err error) {
	m.mu.Lock()
	m.scanErr = err
	m.mu.Unlock()
}

func (m *MockManager) Scan(_ context.Context, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.scanErr
}

func (m *MockManager) Devices(_ context.Context) ([]Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.devicesErr != nil {
		return nil, m.devicesErr
	}
	out := make([]Device, 0, len(m.devices))
	for _, d := range m.devices {
		out = append(out, d)
	}
	return out, nil
}

func (m *MockManager) Connect(_ context.Context, mac string) error {
	m.mu.Lock()
	if m.connectFailN > 0 {
		m.connectFailN--
		err := m.connectErr
		m.mu.Unlock()
		return err
	}
	d, ok := m.devices[mac]
	if !ok {
		m.mu.Unlock()
		return &DeviceNotFoundError{MAC: mac}
	}
	d.Connected = true
	m.devices[mac] = d
	m.mu.Unlock()
	// Non-blocking send: drop if channel is full (capacity is 16).
	select {
	case m.events <- Event{MAC: mac, Type: "connected"}:
	default:
	}
	return nil
}

func (m *MockManager) Disconnect(_ context.Context, mac string) error {
	m.mu.Lock()
	if m.disconnectErr != nil {
		err := m.disconnectErr
		m.mu.Unlock()
		return err
	}
	d, ok := m.devices[mac]
	if !ok {
		m.mu.Unlock()
		return &DeviceNotFoundError{MAC: mac}
	}
	d.Connected = false
	m.devices[mac] = d
	m.mu.Unlock()
	select {
	case m.events <- Event{MAC: mac, Type: "disconnected"}:
	default:
	}
	return nil
}

func (m *MockManager) Pair(_ context.Context, mac string) error {
	m.mu.Lock()
	if m.pairFailN > 0 {
		m.pairFailN--
		err := m.pairErr
		m.mu.Unlock()
		return err
	}
	d, ok := m.devices[mac]
	if !ok {
		m.mu.Unlock()
		return &DeviceNotFoundError{MAC: mac}
	}
	d.Paired = true
	m.devices[mac] = d
	m.mu.Unlock()
	select {
	case m.events <- Event{MAC: mac, Type: "paired"}:
	default:
	}
	return nil
}

func (m *MockManager) Forget(_ context.Context, mac string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.forgetErr != nil {
		return m.forgetErr
	}
	if _, ok := m.devices[mac]; !ok {
		return &DeviceNotFoundError{MAC: mac}
	}
	delete(m.devices, mac)
	return nil
}

func (m *MockManager) SetDiscoverable(_ context.Context, _ bool, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.discoverErr
}

func (m *MockManager) Events() <-chan Event {
	return m.events
}
