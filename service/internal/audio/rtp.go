package audio

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

const pactlTimeout = 3 * time.Second

// moduleIDRegex matches a line that contains only an integer (with optional surrounding whitespace).
var moduleIDRegex = regexp.MustCompile(`^\s*([0-9]+)\s*$`)

func runPactl(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, pactlTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pactl", args...)
	cmd.Env = append(os.Environ(),
		"PIPEWIRE_RUNTIME_DIR=/run/pipewire",
		"PULSE_SERVER=unix:/run/pipewire/pulse-native",
	)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	err := cmd.Run()
	if ctx.Err() != nil {
		log.Printf("pactl %v: context done (%v) after %v", args, ctx.Err(), pactlTimeout)
		return nil, fmt.Errorf("pactl: context done: %w", ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("pactl %v: %w", args, err)
	}
	return stdout.Bytes(), nil
}

// parseModuleID extracts the integer module ID from pactl load-module stdout.
// It scans line by line and returns the first line that is purely an integer.
func parseModuleID(output []byte) (int, error) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		if m := moduleIDRegex.FindStringSubmatch(scanner.Text()); m != nil {
			id, err := strconv.Atoi(m[1])
			if err != nil {
				return 0, fmt.Errorf("pactl: module ID parse error: %w", err)
			}
			return id, nil
		}
	}
	return 0, fmt.Errorf("pactl: no module ID found in output")
}

// AddRTPSink loads a module-rtp-send pactl module targeting destIP:port.
// Returns the pactl module ID assigned by PipeWire-Pulse.
func AddRTPSink(ctx context.Context, destIP string, port int) (int, error) {
	out, err := runPactl(ctx,
		"load-module", "module-rtp-send",
		"source=main-mix.monitor",
		fmt.Sprintf("destination_ip=%s", destIP),
		fmt.Sprintf("port=%d", port),
		"format=s16be", "channels=2", "rate=48000",
	)
	if err != nil {
		return 0, err
	}
	return parseModuleID(out)
}

// RemoveRTPSink unloads the pactl module with the given ID.
func RemoveRTPSink(ctx context.Context, moduleID int) error {
	_, err := runPactl(ctx, "unload-module", fmt.Sprintf("%d", moduleID))
	return err
}

// controller wrappers satisfy the Controller interface.

func (c *controller) AddRTPSink(ctx context.Context, destIP string, port int) (int, error) {
	return AddRTPSink(ctx, destIP, port)
}

func (c *controller) RemoveRTPSink(ctx context.Context, moduleID int) error {
	return RemoveRTPSink(ctx, moduleID)
}
