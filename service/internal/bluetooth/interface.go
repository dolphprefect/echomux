package bluetooth

import (
	"context"
	"time"
)

type Device struct {
	MAC       string
	Name      string
	Connected bool
	Paired    bool
}

type Event struct {
	MAC  string
	Type string // "connected" | "disconnected" | "paired"
}

type Manager interface {
	Scan(ctx context.Context, timeout time.Duration) error
	Devices(ctx context.Context) ([]Device, error)
	Connect(ctx context.Context, mac string) error
	Disconnect(ctx context.Context, mac string) error
	Pair(ctx context.Context, mac string) error
	// Forget unpairs the device and removes it from BlueZ's known-device list.
	Forget(ctx context.Context, mac string) error
	SetDiscoverable(ctx context.Context, enabled bool, timeout time.Duration) error
	Events() <-chan Event
}
