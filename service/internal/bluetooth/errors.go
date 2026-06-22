package bluetooth

import "fmt"

type DeviceNotFoundError struct {
	MAC string
}

func (e *DeviceNotFoundError) Error() string {
	return fmt.Sprintf("device not found: %s", e.MAC)
}
