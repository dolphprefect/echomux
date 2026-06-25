package audio

import "context"

type Node struct {
	ID   int
	MAC  string
	Name string
}

// LinkInfo represents a PipeWire link between two nodes.
// State is one of: "init", "negotiating", "allocating", "paused", "active", "error".
type LinkInfo struct {
	ID           int
	OutputNodeID int
	InputNodeID  int
	State        string
}

// Snapshot holds everything parsed from a single pw-dump invocation.
type Snapshot struct {
	Nodes       []Node         // BT A2DP sink nodes
	Sources     []Node         // Audio/Source nodes (rtp-source, monitors, etc.)
	Links       []LinkInfo
	NodesByName map[string]int // all nodes: node.name → node.ID
}

type Controller interface {
	// Snapshot returns nodes, sources, and links parsed from a single pw-dump call.
	Snapshot(ctx context.Context) (Snapshot, error)
	// Nodes returns all Bluetooth A2DP sink nodes currently in the PipeWire graph.
	Nodes(ctx context.Context) ([]Node, error)
	// Sources returns all Audio/Source nodes (rtp-source and BT input from a phone).
	Sources(ctx context.Context) ([]Node, error)
	// NodeByName returns the PipeWire node ID for the named node, or -1 if not found.
	NodeByName(ctx context.Context, name string) (int, error)
	// Links returns all PipeWire links in the graph with their current state.
	Links(ctx context.Context) ([]LinkInfo, error)
	// Link creates a pw-link from srcNodeID to dstNodeID.
	Link(ctx context.Context, srcNodeID, dstNodeID int) error
	// Unlink removes a pw-link from srcNodeID to dstNodeID.
	Unlink(ctx context.Context, srcNodeID, dstNodeID int) error
	SetVolume(ctx context.Context, nodeID int, level int) error
	SetMute(ctx context.Context, nodeID int, muted bool) error
	// GetVolume returns the current volume [0-100] for the given PipeWire node ID.
	GetVolume(ctx context.Context, nodeID int) (int, error)
	// AddRTPSink loads a module-rtp-send module via pactl and returns the module ID.
	AddRTPSink(ctx context.Context, destIP string, port int) (int, error)
	// RemoveRTPSink unloads the module-rtp-send module with the given ID via pactl.
	RemoveRTPSink(ctx context.Context, moduleID int) error
	// CleanOrphanRTPModules unloads all module-rtp-send instances matching the target port.
	CleanOrphanRTPModules(ctx context.Context, rtpPort int) error
	// ReloadRTPSource kills any existing rtp-source pw-cli subprocess and spawns a fresh
	// one, loading libpipewire-module-rtp-source on the given port. Called by the satellite
	// on each reconnect to master — the PipeWire session does not auto-recover after the
	// RTP stream restarts with a new SSRC.
	ReloadRTPSource(ctx context.Context, port int) error
}

type Executor interface {
	Run(cmd string, args ...string) ([]byte, error)
}
