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
	"strings"
	"time"
)

const pactlTimeout = 3 * time.Second

// moduleIDRegex matches a line that contains only an integer (with optional surrounding whitespace).
var moduleIDRegex = regexp.MustCompile(`^\s*([0-9]+)\s*$`)

func runPactl(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, pactlTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pactl", args...)
	pulseServer := os.Getenv("ECHOMUX_PULSE_SERVER")
	if pulseServer == "" {
		pulseServer = "unix:/run/pipewire/pulse-native"
	}
	cmd.Env = append(os.Environ(),
		"PIPEWIRE_RUNTIME_DIR=/run/pipewire",
		"PULSE_SERVER="+pulseServer,
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

// CleanOrphanRTPModules lists all loaded modules and unloads any module-rtp-send
// module whose arguments contain port=<rtpPort>.
func CleanOrphanRTPModules(ctx context.Context, rtpPort int) error {
	out, err := runPactl(ctx, "list", "modules", "short")
	if err != nil {
		return fmt.Errorf("list modules: %w", err)
	}

	ids := parseOrphanRTPModules(out, rtpPort)
	for _, id := range ids {
		log.Printf("CleanOrphanRTPModules: unloading orphan module %d", id)
		if err := RemoveRTPSink(ctx, id); err != nil {
			log.Printf("CleanOrphanRTPModules: failed to unload module %d: %v", id, err)
		}
	}
	return nil
}

func parseOrphanRTPModules(output []byte, rtpPort int) []int {
	portArg := fmt.Sprintf("port=%d", rtpPort)
	var ids []int
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		moduleIDStr := fields[0]
		moduleName := fields[1]
		if moduleName != "module-rtp-send" {
			continue
		}
		// Check arguments
		argsStr := strings.Join(fields[2:], " ")
		if strings.Contains(argsStr, portArg) {
			moduleID, err := strconv.Atoi(moduleIDStr)
			if err == nil {
				ids = append(ids, moduleID)
			}
		}
	}
	return ids
}

func (c *controller) CleanOrphanRTPModules(ctx context.Context, rtpPort int) error {
	return CleanOrphanRTPModules(ctx, rtpPort)
}
