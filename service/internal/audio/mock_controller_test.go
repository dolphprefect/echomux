package audio_test

import (
	"context"
	"testing"

	"github.com/dolphprefect/echomux/internal/audio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockController_Nodes(t *testing.T) {
	nodes := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.1"}}
	c := audio.NewMockController(nodes)

	out, err := c.Nodes(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, 42, out[0].ID)
	assert.Equal(t, "AA:BB:CC:DD:EE:FF", out[0].MAC)
}

func TestMockController_SetSinks(t *testing.T) {
	c := audio.NewMockController(nil)
	nodes := []audio.Node{{ID: 77, MAC: "11:22:33:44:55:66", Name: "bluez_output.2"}}
	c.SetSinks(nodes)

	out, _ := c.Nodes(context.Background())
	require.Len(t, out, 1)
	assert.Equal(t, 77, out[0].ID)
}

func TestMockController_Sources_DefaultRTPSource(t *testing.T) {
	c := audio.NewMockController(nil)

	sources, err := c.Sources(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)
	assert.Equal(t, 99, sources[0].ID)
	assert.Equal(t, "rtp-source", sources[0].Name)
}

func TestMockController_SetSources(t *testing.T) {
	c := audio.NewMockController(nil)
	newSources := []audio.Node{{ID: 55, Name: "custom-source"}}
	c.SetSources(newSources)

	sources, err := c.Sources(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)
	assert.Equal(t, 55, sources[0].ID)
}

func TestMockController_Sources_WithError(t *testing.T) {
	c := audio.NewMockController(nil)
	c.SetSourcesErr(assert.AnError)

	_, err := c.Sources(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

func TestMockController_Sources_ErrorReturnedBySourcesCall(t *testing.T) {
	c := audio.NewMockController(nil)
	c.SetSourcesErr(assert.AnError)

	_, err := c.Sources(context.Background())
	assert.ErrorIs(t, err, assert.AnError)

	// Snapshot calls Sources internally but discards the error.
	// It still returns a valid (possibly empty) Snapshot.
	snap, snapErr := c.Snapshot(context.Background())
	assert.NoError(t, snapErr)
	assert.Empty(t, snap.Sources)
}

func TestMockController_Snapshot(t *testing.T) {
	nodes := []audio.Node{
		{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.1"},
	}
	c := audio.NewMockController(nodes)
	c.SetPWLinks([]audio.LinkInfo{
		{ID: 1, OutputNodeID: 99, InputNodeID: 42, State: "active"},
	})

	snap, err := c.Snapshot(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Nodes, 1)
	assert.Equal(t, "bluez_output.1", snap.Nodes[0].Name)
	assert.Len(t, snap.Sources, 1)
	assert.Equal(t, "rtp-source", snap.Sources[0].Name)
	assert.Len(t, snap.Links, 1)
	assert.Equal(t, "active", snap.Links[0].State)
}

func TestMockController_Snapshot_NodesByNameMergesAll(t *testing.T) {
	nodes := []audio.Node{
		{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.1"},
	}
	c := audio.NewMockController(nodes)
	c.AddNamedNode("main-mix", audio.Node{ID: 200, Name: "main-mix"})

	snap, err := c.Snapshot(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 42, snap.NodesByName["bluez_output.1"])
	assert.Equal(t, 99, snap.NodesByName["rtp-source"])
	assert.Equal(t, 200, snap.NodesByName["main-mix"])
}

func TestMockController_NodeByName(t *testing.T) {
	nodes := []audio.Node{{ID: 42, MAC: "AA:BB:CC:DD:EE:FF", Name: "bluez_output.1"}}
	c := audio.NewMockController(nodes)

	id, err := c.NodeByName(context.Background(), "bluez_output.1")
	require.NoError(t, err)
	assert.Equal(t, 42, id)

	id, err = c.NodeByName(context.Background(), "rtp-source")
	require.NoError(t, err)
	assert.Equal(t, 99, id)

	id, err = c.NodeByName(context.Background(), "nonexistent")
	require.NoError(t, err)
	assert.Equal(t, -1, id)
}

func TestMockController_AddNamedNode(t *testing.T) {
	c := audio.NewMockController(nil)
	c.AddNamedNode("custom", audio.Node{ID: 123, Name: "custom"})

	id, err := c.NodeByName(context.Background(), "custom")
	require.NoError(t, err)
	assert.Equal(t, 123, id)
}

func TestMockController_SetVolume_GetVolume(t *testing.T) {
	c := audio.NewMockController(nil)

	err := c.SetVolume(context.Background(), 42, 75)
	require.NoError(t, err)

	v, err := c.GetVolume(context.Background(), 42)
	require.NoError(t, err)
	assert.Equal(t, 75, v)

	// Unset volume defaults to 100.
	v, err = c.GetVolume(context.Background(), 99)
	require.NoError(t, err)
	assert.Equal(t, 100, v)
}

func TestMockController_SetVolume_OutOfRange(t *testing.T) {
	c := audio.NewMockController(nil)
	assert.Error(t, c.SetVolume(context.Background(), 42, 101))
	assert.Error(t, c.SetVolume(context.Background(), 42, -1))
}

func TestMockController_SetMute(t *testing.T) {
	c := audio.NewMockController(nil)

	require.NoError(t, c.SetMute(context.Background(), 42, true))
	assert.True(t, c.Muted(42))

	require.NoError(t, c.SetMute(context.Background(), 42, false))
	assert.False(t, c.Muted(42))
}

func TestMockController_Volume_Muted_Accessors(t *testing.T) {
	c := audio.NewMockController(nil)

	assert.Equal(t, 0, c.Volume(42))  // unset defaults to 0
	assert.False(t, c.Muted(42))     // unset defaults to false

	c.SetVolume(context.Background(), 42, 80) //nolint
	assert.Equal(t, 80, c.Volume(42))
}

func TestMockController_Link_Unlink_Linked(t *testing.T) {
	c := audio.NewMockController(nil)

	assert.False(t, c.Linked(42))

	require.NoError(t, c.Link(context.Background(), 99, 42))
	assert.True(t, c.Linked(42))

	require.NoError(t, c.Unlink(context.Background(), 99, 42))
	assert.False(t, c.Linked(42))
}

func TestMockController_Linked_PartialMatch(t *testing.T) {
	c := audio.NewMockController(nil)

	// Link 99→42 should not make 43 show as linked.
	require.NoError(t, c.Link(context.Background(), 99, 42))
	assert.True(t, c.Linked(42))
	assert.False(t, c.Linked(43))
}

func TestMockController_SetPWLinks_Links(t *testing.T) {
	c := audio.NewMockController(nil)

	links := []audio.LinkInfo{
		{ID: 1, OutputNodeID: 10, InputNodeID: 42, State: "active"},
		{ID: 2, OutputNodeID: 10, InputNodeID: 77, State: "paused"},
	}
	c.SetPWLinks(links)

	out, err := c.Links(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, "active", out[0].State)
	assert.Equal(t, "paused", out[1].State)
}

func TestMockController_SetPWLinks_Empty(t *testing.T) {
	c := audio.NewMockController(nil)

	out, err := c.Links(context.Background())
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestMockController_SetPWLinks_DoesNotAffectLinked(t *testing.T) {
	c := audio.NewMockController(nil)

	require.NoError(t, c.Link(context.Background(), 99, 42))
	assert.True(t, c.Linked(42))

	c.SetPWLinks([]audio.LinkInfo{})
	assert.True(t, c.Linked(42), "SetPWLinks should not clear the Link/Unlink-based linked state")
}
