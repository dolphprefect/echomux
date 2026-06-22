package audio_test

import (
	"context"
	"os"
	"testing"

	"github.com/dolphprefect/echomux/internal/audio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type spyExecutor struct {
	calls   [][]string
	outputs [][]byte
	errors  []error
	idx     int
}

func (s *spyExecutor) Run(cmd string, args ...string) ([]byte, error) {
	s.calls = append(s.calls, append([]string{cmd}, args...))
	defer func() { s.idx++ }()
	if s.idx < len(s.outputs) {
		var err error
		if s.idx < len(s.errors) {
			err = s.errors[s.idx]
		}
		return s.outputs[s.idx], err
	}
	return nil, nil
}

func (s *spyExecutor) lastCall() []string {
	if len(s.calls) == 0 {
		return nil
	}
	return s.calls[len(s.calls)-1]
}

func TestController_SetVolume(t *testing.T) {
	spy := &spyExecutor{}
	c := audio.NewController(spy)

	err := c.SetVolume(context.Background(), 42, 75)
	require.NoError(t, err)

	call := spy.lastCall()
	assert.Equal(t, "wpctl", call[0])
	assert.Equal(t, "set-volume", call[1])
	assert.Equal(t, "42", call[2])
	assert.Equal(t, "0.75", call[3])
}

func TestController_SetVolume_Boundaries(t *testing.T) {
	spy := &spyExecutor{}
	c := audio.NewController(spy)
	ctx := context.Background()

	require.NoError(t, c.SetVolume(ctx, 1, 0))
	assert.Equal(t, "0.00", spy.lastCall()[3])

	require.NoError(t, c.SetVolume(ctx, 1, 100))
	assert.Equal(t, "1.00", spy.lastCall()[3])

	require.Error(t, c.SetVolume(ctx, 1, 101))
	require.Error(t, c.SetVolume(ctx, 1, -1))
}

func TestController_SetMute(t *testing.T) {
	spy := &spyExecutor{}
	c := audio.NewController(spy)
	ctx := context.Background()

	require.NoError(t, c.SetMute(ctx, 42, true))
	call := spy.lastCall()
	assert.Equal(t, "wpctl", call[0])
	assert.Equal(t, "set-mute", call[1])
	assert.Equal(t, "42", call[2])
	assert.Equal(t, "1", call[3])

	require.NoError(t, c.SetMute(ctx, 42, false))
	assert.Equal(t, "0", spy.lastCall()[3])
}

func TestController_Link(t *testing.T) {
	spy := &spyExecutor{}
	c := audio.NewController(spy)

	err := c.Link(context.Background(), 99, 42)
	require.NoError(t, err)

	call := spy.lastCall()
	assert.Equal(t, "pw-link", call[0])
	assert.Equal(t, "99", call[1])
	assert.Equal(t, "42", call[2])
}

func TestController_Unlink(t *testing.T) {
	spy := &spyExecutor{}
	c := audio.NewController(spy)

	err := c.Unlink(context.Background(), 99, 42)
	require.NoError(t, err)

	call := spy.lastCall()
	assert.Equal(t, "pw-link", call[0])
	assert.Equal(t, "-d", call[1])
	assert.Equal(t, "99", call[2])
	assert.Equal(t, "42", call[3])
}

func TestController_Sources(t *testing.T) {
	data, err := os.ReadFile("testdata/pw-dump.json")
	require.NoError(t, err)
	spy := &spyExecutor{outputs: [][]byte{data}}

	c := audio.NewController(spy)
	sources, err := c.Sources(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)
	assert.Equal(t, 99, sources[0].ID)
	assert.Equal(t, "rtp-source", sources[0].Name)
}

func TestController_Nodes(t *testing.T) {
	data, err := os.ReadFile("testdata/pw-dump.json")
	require.NoError(t, err)
	spy := &spyExecutor{outputs: [][]byte{data}}

	c := audio.NewController(spy)
	nodes, err := c.Nodes(context.Background())
	require.NoError(t, err)
	require.Len(t, nodes, 2)
}

func TestController_GetVolume(t *testing.T) {
	spy := &spyExecutor{outputs: [][]byte{[]byte("Volume: 0.75\n")}}
	c := audio.NewController(spy)

	v, err := c.GetVolume(context.Background(), 42)
	require.NoError(t, err)
	assert.Equal(t, 75, v)

	call := spy.lastCall()
	assert.Equal(t, "wpctl", call[0])
	assert.Equal(t, "get-volume", call[1])
	assert.Equal(t, "42", call[2])
}

func TestController_GetVolume_Muted(t *testing.T) {
	spy := &spyExecutor{outputs: [][]byte{[]byte("Volume: 1.00 [MUTED]\n")}}
	c := audio.NewController(spy)

	v, err := c.GetVolume(context.Background(), 42)
	require.NoError(t, err)
	assert.Equal(t, 100, v)
}

func TestController_GetVolume_EmptyOutput(t *testing.T) {
	spy := &spyExecutor{outputs: [][]byte{[]byte("")}}
	c := audio.NewController(spy)

	_, err := c.GetVolume(context.Background(), 42)
	require.Error(t, err, "empty wpctl output should return error, not panic")
}

func TestController_GetVolume_UnexpectedFormat(t *testing.T) {
	spy := &spyExecutor{outputs: [][]byte{[]byte("not a volume line\n")}}
	c := audio.NewController(spy)

	_, err := c.GetVolume(context.Background(), 42)
	require.Error(t, err)
}

func TestController_Snapshot(t *testing.T) {
	data, err := os.ReadFile("testdata/pw-dump.json")
	require.NoError(t, err)
	// Snapshot calls pw-dump once; supply the same output for that single call.
	spy := &spyExecutor{outputs: [][]byte{data}}

	c := audio.NewController(spy)
	snap, err := c.Snapshot(context.Background())
	require.NoError(t, err)

	assert.Len(t, snap.Nodes, 2)
	assert.Len(t, snap.Sources, 1)
	assert.Equal(t, "rtp-source", snap.Sources[0].Name)
	assert.Len(t, snap.Links, 4)
	assert.Equal(t, 42, snap.NodesByName["bluez_output.AA_BB_CC_DD_EE_FF.1"])
	assert.Equal(t, 99, snap.NodesByName["rtp-source"])
	// Snapshot must use exactly one pw-dump call.
	assert.Len(t, spy.calls, 1, "Snapshot should invoke pw-dump exactly once")
}

func TestController_NodeByName(t *testing.T) {
	data, err := os.ReadFile("testdata/pw-dump.json")
	require.NoError(t, err)
	spy := &spyExecutor{outputs: [][]byte{data}}

	c := audio.NewController(spy)
	id, err := c.NodeByName(context.Background(), "rtp-source")
	require.NoError(t, err)
	assert.Equal(t, 99, id)
}

func TestController_NodeByName_Missing(t *testing.T) {
	spy := &spyExecutor{outputs: [][]byte{[]byte("[]")}}
	c := audio.NewController(spy)

	id, err := c.NodeByName(context.Background(), "nonexistent")
	require.NoError(t, err)
	assert.Equal(t, -1, id)
}

func TestController_Links(t *testing.T) {
	data, err := os.ReadFile("testdata/pw-dump.json")
	require.NoError(t, err)
	spy := &spyExecutor{outputs: [][]byte{data}}

	c := audio.NewController(spy)
	links, err := c.Links(context.Background())
	require.NoError(t, err)
	assert.Len(t, links, 4)

	call := spy.lastCall()
	assert.Equal(t, "pw-dump", call[0])
	assert.Equal(t, "--no-colors", call[1])
}
