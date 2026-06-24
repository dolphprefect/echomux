package audio

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type MockController struct {
	mu           sync.Mutex
	sinks        []Node
	sources      []Node
	namedNodes   map[string]Node
	volumes      map[int]int
	mutes        map[int]bool
	links        map[string]bool // "srcID->dstID"
	pwLinks      []LinkInfo      // injectable PW link states for watchdog tests
	sourcesErr   error
	nodesErr     error
	snapshotErr  error
	volumeErr    error
	muteErr      error
	rtpModuleID  int
	rtpAddErr    error
	rtpRemoveErr error
}

func NewMockController(sinks []Node) *MockController {
	return &MockController{
		sinks:      sinks,
		sources:    []Node{{ID: 99, Name: "rtp-source"}},
		namedNodes: make(map[string]Node),
		volumes:    make(map[int]int),
		mutes:      make(map[int]bool),
		links:      make(map[string]bool),
	}
}

func (c *MockController) SetSources(sources []Node) {
	c.mu.Lock()
	c.sources = sources
	c.mu.Unlock()
}

// SetNodesErr causes Nodes() to return err. Also affects Snapshot().
func (c *MockController) SetNodesErr(err error) {
	c.mu.Lock()
	c.nodesErr = err
	c.mu.Unlock()
}

// SetSnapshotErr causes Snapshot() to return err directly.
func (c *MockController) SetSnapshotErr(err error) {
	c.mu.Lock()
	c.snapshotErr = err
	c.mu.Unlock()
}

// SetVolumeErr causes SetVolume() to return err.
func (c *MockController) SetVolumeErr(err error) {
	c.mu.Lock()
	c.volumeErr = err
	c.mu.Unlock()
}

// SetMuteErr causes SetMute() to return err.
func (c *MockController) SetMuteErr(err error) {
	c.mu.Lock()
	c.muteErr = err
	c.mu.Unlock()
}

// SetSourcesErr causes Sources() to return err. Also affects Snapshot() since
// it calls Sources() internally.
func (c *MockController) SetSourcesErr(err error) {
	c.mu.Lock()
	c.sourcesErr = err
	c.mu.Unlock()
}

func (c *MockController) SetSinks(sinks []Node) {
	c.mu.Lock()
	c.sinks = sinks
	c.mu.Unlock()
}

// AddNamedNode registers a node that can be found by NodeByName.
func (c *MockController) AddNamedNode(name string, n Node) {
	c.mu.Lock()
	c.namedNodes[name] = n
	c.mu.Unlock()
}

func (c *MockController) Snapshot(ctx context.Context) (Snapshot, error) {
	c.mu.Lock()
	snapshotErr := c.snapshotErr
	c.mu.Unlock()
	if snapshotErr != nil {
		return Snapshot{}, snapshotErr
	}
	nodes, _ := c.Nodes(ctx)
	sources, _ := c.Sources(ctx)
	links, _ := c.Links(ctx)
	byName := make(map[string]int)
	for _, n := range append(nodes, sources...) {
		if n.Name != "" {
			byName[n.Name] = n.ID
		}
	}
	c.mu.Lock()
	for name, n := range c.namedNodes {
		byName[name] = n.ID
	}
	c.mu.Unlock()
	return Snapshot{Nodes: nodes, Sources: sources, Links: links, NodesByName: byName}, nil
}

func (c *MockController) Nodes(_ context.Context) ([]Node, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.nodesErr != nil {
		return nil, c.nodesErr
	}
	out := make([]Node, len(c.sinks))
	copy(out, c.sinks)
	return out, nil
}

func (c *MockController) Sources(_ context.Context) ([]Node, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sourcesErr != nil {
		return nil, c.sourcesErr
	}
	out := make([]Node, len(c.sources))
	copy(out, c.sources)
	return out, nil
}

func (c *MockController) NodeByName(_ context.Context, name string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n, ok := c.namedNodes[name]; ok {
		return n.ID, nil
	}
	for _, n := range c.sinks {
		if n.Name == name {
			return n.ID, nil
		}
	}
	for _, n := range c.sources {
		if n.Name == name {
			return n.ID, nil
		}
	}
	return -1, nil
}

func (c *MockController) Link(_ context.Context, srcID, dstID int) error {
	c.mu.Lock()
	c.links[fmt.Sprintf("%d->%d", srcID, dstID)] = true
	c.mu.Unlock()
	return nil
}

func (c *MockController) Unlink(_ context.Context, srcID, dstID int) error {
	c.mu.Lock()
	c.links[fmt.Sprintf("%d->%d", srcID, dstID)] = false
	c.mu.Unlock()
	return nil
}

func (c *MockController) SetVolume(_ context.Context, nodeID, level int) error {
	if level < 0 || level > 100 {
		return fmt.Errorf("volume %d out of range [0,100]", level)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.volumeErr != nil {
		return c.volumeErr
	}
	c.volumes[nodeID] = level
	return nil
}

func (c *MockController) SetMute(_ context.Context, nodeID int, muted bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.muteErr != nil {
		return c.muteErr
	}
	c.mutes[nodeID] = muted
	return nil
}

func (c *MockController) GetVolume(_ context.Context, nodeID int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.volumes[nodeID]; ok {
		return v, nil
	}
	return 100, nil
}

// Linked returns true if dstID (sink) has any active incoming link.
// SetPWLinks sets the PipeWire link states returned by Links() and Snapshot().
func (c *MockController) SetPWLinks(links []LinkInfo) {
	c.mu.Lock()
	c.pwLinks = links
	c.mu.Unlock()
}

func (c *MockController) Links(_ context.Context) ([]LinkInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]LinkInfo, len(c.pwLinks))
	copy(out, c.pwLinks)
	return out, nil
}

// SetRTPAddResult configures the module ID and error returned by AddRTPSink.
func (c *MockController) SetRTPAddResult(moduleID int, err error) {
	c.mu.Lock()
	c.rtpModuleID = moduleID
	c.rtpAddErr = err
	c.mu.Unlock()
}

// SetRTPRemoveErr configures the error returned by RemoveRTPSink.
func (c *MockController) SetRTPRemoveErr(err error) {
	c.mu.Lock()
	c.rtpRemoveErr = err
	c.mu.Unlock()
}

func (c *MockController) AddRTPSink(_ context.Context, _ string, _ int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rtpModuleID, c.rtpAddErr
}

func (c *MockController) RemoveRTPSink(_ context.Context, _ int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rtpRemoveErr
}

func (c *MockController) Linked(dstID int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	suffix := fmt.Sprintf("->%d", dstID)
	for k, v := range c.links {
		if strings.HasSuffix(k, suffix) && v {
			return true
		}
	}
	return false
}

func (c *MockController) Volume(nodeID int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.volumes[nodeID]
}

func (c *MockController) Muted(nodeID int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mutes[nodeID]
}
