package audio

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type controller struct {
	exec Executor
}

func NewController(exec Executor) Controller {
	return &controller{exec: exec}
}

func (c *controller) Snapshot(_ context.Context) (Snapshot, error) {
	out, err := c.exec.Run("pw-dump", "--no-colors")
	if err != nil {
		return Snapshot{}, fmt.Errorf("pw-dump: %w", err)
	}
	nodes, err := ParseNodes(out)
	if err != nil {
		return Snapshot{}, err
	}
	sources, err := ParseSources(out)
	if err != nil {
		return Snapshot{}, err
	}
	links, err := ParseLinks(out)
	if err != nil {
		return Snapshot{}, err
	}
	byName, err := ParseNodesByName(out)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Nodes: nodes, Sources: sources, Links: links, NodesByName: byName}, nil
}

func (c *controller) Nodes(_ context.Context) ([]Node, error) {
	out, err := c.exec.Run("pw-dump", "--no-colors")
	if err != nil {
		return nil, fmt.Errorf("pw-dump: %w", err)
	}
	return ParseNodes(out)
}

func (c *controller) Sources(_ context.Context) ([]Node, error) {
	out, err := c.exec.Run("pw-dump", "--no-colors")
	if err != nil {
		return nil, fmt.Errorf("pw-dump: %w", err)
	}
	return ParseSources(out)
}

func (c *controller) Links(_ context.Context) ([]LinkInfo, error) {
	out, err := c.exec.Run("pw-dump", "--no-colors")
	if err != nil {
		return nil, fmt.Errorf("pw-dump: %w", err)
	}
	return ParseLinks(out)
}

func (c *controller) NodeByName(_ context.Context, name string) (int, error) {
	out, err := c.exec.Run("pw-dump", "--no-colors")
	if err != nil {
		return -1, fmt.Errorf("pw-dump: %w", err)
	}
	return ParseNodeByName(out, name)
}

func (c *controller) Link(_ context.Context, srcID, dstID int) error {
	_, err := c.exec.Run("pw-link", fmt.Sprintf("%d", srcID), fmt.Sprintf("%d", dstID))
	return err
}

func (c *controller) Unlink(_ context.Context, srcID, dstID int) error {
	_, err := c.exec.Run("pw-link", "-d", fmt.Sprintf("%d", srcID), fmt.Sprintf("%d", dstID))
	return err
}

func (c *controller) SetVolume(_ context.Context, nodeID, level int) error {
	if level < 0 || level > 100 {
		return fmt.Errorf("volume %d out of range [0,100]", level)
	}
	_, err := c.exec.Run("wpctl", "set-volume", fmt.Sprintf("%d", nodeID), fmt.Sprintf("%.2f", float64(level)/100))
	return err
}

func (c *controller) SetMute(_ context.Context, nodeID int, muted bool) error {
	val := "0"
	if muted {
		val = "1"
	}
	_, err := c.exec.Run("wpctl", "set-mute", fmt.Sprintf("%d", nodeID), val)
	return err
}

func (c *controller) GetVolume(_ context.Context, nodeID int) (int, error) {
	// wpctl get-volume <id> → "Volume: 0.08\n" or "Volume: 1.00 [MUTED]\n"
	out, err := c.exec.Run("wpctl", "get-volume", fmt.Sprintf("%d", nodeID))
	if err != nil {
		return 0, err
	}
	// Parse "Volume: 0.08" or "Volume: 1.00 [MUTED]"
	line := strings.TrimSpace(string(out))
	line = strings.TrimPrefix(line, "Volume: ")
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return 0, fmt.Errorf("GetVolume: unexpected output %q", string(out))
	}
	f, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("GetVolume: unexpected output %q", string(out))
	}
	return int(math.Round(f * 100)), nil
}
