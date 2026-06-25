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
	"syscall"
	"time"
)

const pwCliRTPLoadTimeout = 5 * time.Second

// moduleRefRegex matches pw-cli output like "N = @module:M".
var moduleRefRegex = regexp.MustCompile(`=\s*(@module:\d+)`)

// killOrphanRTPSinkProcesses kills any pw-cli processes left over from a previous
// echomux instance. Called once at startup on the Master before accepting satellites,
// so orphaned RTP sink modules are removed before new ones are created.
func killOrphanRTPSinkProcesses() {
	_ = exec.Command("pkill", "-KILL", "-x", "pw-cli").Run()
	time.Sleep(200 * time.Millisecond)
}

// rtpSinkPayload returns the pw-cli stdin command that loads libpipewire-module-rtp-sink.
// Exported as a pure function so tests can verify the command format without spawning processes.
func rtpSinkPayload(destIP string, port int) string {
	return fmt.Sprintf(
		"load-module libpipewire-module-rtp-sink "+
			`{"audio.format":"S16BE","audio.channels":2,"audio.rate":48000,`+
			`"destination.ip":"%s","destination.port":%d,`+
			`"source.name":"rtp-sink","stream.props":{"target.object":"main-mix-source"}}`,
		destIP, port,
	)
}

// parseModuleRef extracts the "@module:N" token from pw-cli load-module output.
// Used for diagnostic logging. The module's actual lifecycle is managed by killing
// the pw-cli subprocess, not by tracking the module reference.
func parseModuleRef(output []byte) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		if m := moduleRefRegex.FindStringSubmatch(scanner.Text()); m != nil {
			return m[1], nil
		}
	}
	return "", fmt.Errorf("pw-cli: no @module:N reference found in output")
}

// spawnRTPSink starts a pw-cli subprocess that loads libpipewire-module-rtp-sink
// and keeps it running. The module remains active until the subprocess is killed.
// Returns the subprocess cmd; caller must eventually call cmd.Process.Kill().
func spawnRTPSink(destIP string, port int) (*exec.Cmd, error) {
	cmd := exec.Command("pw-cli")
	cmd.Env = append(os.Environ(), "PIPEWIRE_RUNTIME_DIR=/run/pipewire")
	// Ensure the subprocess dies if echomux is killed, preventing orphaned RTP sinks.
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("rtp-sink: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("rtp-sink: stdout pipe: %w", err)
	}
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("rtp-sink: start: %w", err)
	}

	// Write load command; leave stdin open so pw-cli keeps running and the module
	// stays loaded. pw-cli treats closing stdin as EOF and exits (unloading the module).
	fmt.Fprintln(stdin, rtpSinkPayload(destIP, port))

	// Read stdout in a goroutine: signal when module ref appears, drain after.
	loaded := make(chan string, 1) // receives @module:N on success, or "" on process exit
	go func() {
		scanner := bufio.NewScanner(stdout)
		found := false
		for scanner.Scan() {
			if !found {
				if m := moduleRefRegex.FindStringSubmatch(scanner.Text()); m != nil {
					found = true
					loaded <- m[1]
					// Keep scanning to drain stdout for the life of the process.
				}
			}
		}
		if !found {
			loaded <- "" // process exited before we saw the module ref
		}
	}()

	select {
	case ref := <-loaded:
		if ref == "" {
			cmd.Process.Kill()
			cmd.Wait()
			return nil, fmt.Errorf("rtp-sink: process exited before module loaded for %s:%d", destIP, port)
		}
		log.Printf("rtp-sink: loaded %s → %s:%d", ref, destIP, port)
		return cmd, nil
	case <-time.After(pwCliRTPLoadTimeout):
		cmd.Process.Kill()
		cmd.Wait()
		return nil, fmt.Errorf("rtp-sink: timeout waiting for module load (%s:%d)", destIP, port)
	}
}

// controller methods implement the Controller interface.

func (c *controller) AddRTPSink(ctx context.Context, destIP string, port int) (int, error) {
	cmd, err := spawnRTPSink(destIP, port)
	if err != nil {
		return 0, err
	}
	c.rtpMu.Lock()
	if c.rtpSinks == nil {
		c.rtpSinks = make(map[int]*exec.Cmd)
	}
	// Kill any existing sink on this port before replacing it.
	if old, ok := c.rtpSinks[port]; ok {
		old.Process.Kill()
		go old.Wait()
	}
	c.rtpSinks[port] = cmd
	c.rtpMu.Unlock()
	return port, nil
}

func (c *controller) RemoveRTPSink(ctx context.Context, moduleID int) error {
	c.rtpMu.Lock()
	cmd, ok := c.rtpSinks[moduleID]
	if ok {
		delete(c.rtpSinks, moduleID)
	}
	c.rtpMu.Unlock()

	if ok && cmd.Process != nil {
		cmd.Process.Kill()
		go cmd.Wait()
	}
	return nil
}

// CleanOrphanRTPModules kills any pw-cli processes surviving from a previous
// echomux instance, then removes any tracked RTP sink for the given port.
// Called once at Master startup (analogous to killOrphanLoopbacks).
func (c *controller) CleanOrphanRTPModules(ctx context.Context, rtpPort int) error {
	killOrphanRTPSinkProcesses()
	return c.RemoveRTPSink(ctx, rtpPort)
}
