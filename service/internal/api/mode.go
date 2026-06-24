package api

import (
	"fmt"
	"net"
)

// Mode controls whether this echomux instance runs standalone, as a master,
// or as a satellite.
type Mode string

const (
	ModeStandalone Mode = "standalone"
	ModeMaster     Mode = "master"
	ModeSatellite  Mode = "satellite"
)

// ParseMode converts a string to a Mode, returning an error for unknown values.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case ModeStandalone, ModeMaster, ModeSatellite:
		return Mode(s), nil
	default:
		return "", fmt.Errorf("invalid mode %q: must be standalone, master, or satellite", s)
	}
}

// ValidateConfig checks that the combination of flags is coherent.
// Call this before NewServer in main to get a clear startup error.
func ValidateConfig(mode Mode, masterAddr string) error {
	if mode == ModeSatellite {
		if masterAddr == "" {
			return fmt.Errorf("satellite mode requires --master-addr (or ECHOMUX_MASTER_ADDR)")
		}
		if _, _, err := net.SplitHostPort(masterAddr); err != nil {
			return fmt.Errorf("--master-addr %q is not a valid host:port address: %w", masterAddr, err)
		}
	}
	return nil
}
