package bluetooth

import (
	"context"
	"fmt"
	"time"

	"github.com/muka/go-bluetooth/api"
	"github.com/muka/go-bluetooth/bluez/profile/adapter"
	"github.com/muka/go-bluetooth/bluez/profile/device"
)

type blueZManager struct {
	adapterID string
	events    chan Event
}

func NewManager(adapterID string) (Manager, error) {
	if _, err := api.GetAdapter(adapterID); err != nil {
		return nil, fmt.Errorf("bluetooth adapter %s: %w", adapterID, err)
	}
	return &blueZManager{
		adapterID: adapterID,
		events:    make(chan Event, 32),
	}, nil
}

func (m *blueZManager) adapter() (*adapter.Adapter1, error) {
	return api.GetAdapter(m.adapterID)
}

func (m *blueZManager) Scan(ctx context.Context, timeout time.Duration) error {
	a, err := m.adapter()
	if err != nil {
		return err
	}
	// Reset any previous filter so BlueZ uses MGMT_OP_START_DISCOVERY (auto
	// BR/EDR+LE) rather than MGMT_OP_START_SERVICE_DISCOVERY. Some Realtek USB
	// adapters (RTL8761BU) fail to return BR/EDR inquiry results with the
	// SERVICE_DISCOVERY path even though the command succeeds.
	if err := a.SetDiscoveryFilter(map[string]interface{}{}); err != nil {
		return fmt.Errorf("SetDiscoveryFilter: %w", err)
	}
	if err := a.StartDiscovery(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case <-time.After(timeout):
	}
	return a.StopDiscovery()
}

func (m *blueZManager) Devices(_ context.Context) ([]Device, error) {
	a, err := m.adapter()
	if err != nil {
		return nil, err
	}
	devs, err := a.GetDevices()
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(devs))
	for _, d := range devs {
		props, err := d.GetProperties()
		if err != nil {
			continue
		}
		out = append(out, Device{
			MAC:       props.Address,
			Name:      props.Name,
			Connected: props.Connected,
			Paired:    props.Paired,
		})
	}
	return out, nil
}

func (m *blueZManager) Connect(_ context.Context, mac string) error {
	d, err := m.deviceByMAC(mac)
	if err != nil {
		return err
	}
	if err := d.Connect(); err != nil {
		return err
	}
	_ = d.SetTrusted(true) // allow BlueZ to auto-reconnect after reboot
	select {
	case m.events <- Event{MAC: mac, Type: "connected"}:
	default:
	}
	return nil
}

func (m *blueZManager) Disconnect(_ context.Context, mac string) error {
	d, err := m.deviceByMAC(mac)
	if err != nil {
		return err
	}
	if err := d.Disconnect(); err != nil {
		return err
	}
	select {
	case m.events <- Event{MAC: mac, Type: "disconnected"}:
	default:
	}
	return nil
}

func (m *blueZManager) Pair(_ context.Context, mac string) error {
	d, err := m.deviceByMAC(mac)
	if err != nil {
		return err
	}
	if err := d.Pair(); err != nil {
		return err
	}
	select {
	case m.events <- Event{MAC: mac, Type: "paired"}:
	default:
	}
	return nil
}

func (m *blueZManager) Forget(_ context.Context, mac string) error {
	a, err := m.adapter()
	if err != nil {
		return err
	}
	d, err := m.deviceByMAC(mac)
	if err != nil {
		return err
	}
	return a.RemoveDevice(d.Path())
}

func (m *blueZManager) SetDiscoverable(_ context.Context, enabled bool, timeout time.Duration) error {
	a, err := m.adapter()
	if err != nil {
		return err
	}
	if err := a.SetPairable(enabled); err != nil {
		return err
	}
	if err := a.SetDiscoverable(enabled); err != nil {
		return err
	}
	return a.SetDiscoverableTimeout(uint32(timeout.Seconds()))
}

func (m *blueZManager) Events() <-chan Event {
	return m.events
}

func (m *blueZManager) deviceByMAC(mac string) (*device.Device1, error) {
	a, err := m.adapter()
	if err != nil {
		return nil, err
	}
	// GetDeviceByAddress uses NewDevice1(path) which reads properties directly
	// from D-Bus, bypassing the object manager cache that fails for classic BT
	// devices with uint8-keyed AdvertisingData.
	dev, err := a.GetDeviceByAddress(mac)
	if err != nil {
		return nil, err
	}
	if dev == nil {
		return nil, &DeviceNotFoundError{MAC: mac}
	}
	return dev, nil
}
