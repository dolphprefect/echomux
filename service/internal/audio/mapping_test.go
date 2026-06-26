package audio_test

import (
	"os"
	"testing"

	"github.com/dolphprefect/echomux/internal/audio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseNodes(t *testing.T) {
	data, err := os.ReadFile("testdata/pw-dump.json")
	require.NoError(t, err)

	nodes, err := audio.ParseNodes(data)
	require.NoError(t, err)

	// Only BT A2DP sinks should be returned
	require.Len(t, nodes, 2)

	byMAC := make(map[string]audio.Node)
	for _, n := range nodes {
		byMAC[n.MAC] = n
	}

	a, ok := byMAC["AA:BB:CC:DD:EE:FF"]
	require.True(t, ok, "Speaker A not found")
	assert.Equal(t, 42, a.ID)

	b, ok := byMAC["11:22:33:44:55:66"]
	require.True(t, ok, "Speaker B not found")
	assert.Equal(t, 77, b.ID)
}

func TestParseNodes_EmptyInput(t *testing.T) {
	nodes, err := audio.ParseNodes([]byte("[]"))
	require.NoError(t, err)
	assert.Empty(t, nodes)
}

func TestParseNodes_InvalidJSON(t *testing.T) {
	_, err := audio.ParseNodes([]byte("{bad json"))
	require.Error(t, err)
}

func TestParseSourceNodeID(t *testing.T) {
	data, err := os.ReadFile("testdata/pw-dump.json")
	require.NoError(t, err)

	id, err := audio.ParseSourceNodeID(data, "rtp-source")
	require.NoError(t, err)
	assert.Equal(t, 99, id)
}

func TestParseSourceNodeID_Missing(t *testing.T) {
	_, err := audio.ParseSourceNodeID([]byte("[]"), "rtp-source")
	require.Error(t, err)
}

func TestParseSources(t *testing.T) {
	data, err := os.ReadFile("testdata/pw-dump.json")
	require.NoError(t, err)

	sources, err := audio.ParseSources(data)
	require.NoError(t, err)
	require.Len(t, sources, 1)
	assert.Equal(t, 99, sources[0].ID)
	assert.Equal(t, "rtp-source", sources[0].Name)
	assert.Equal(t, "", sources[0].MAC)
}

func TestParseSources_Empty(t *testing.T) {
	sources, err := audio.ParseSources([]byte("[]"))
	require.NoError(t, err)
	assert.Empty(t, sources)
}

func TestParseLinks(t *testing.T) {
	data, err := os.ReadFile("testdata/pw-dump.json")
	require.NoError(t, err)

	links, err := audio.ParseLinks(data)
	require.NoError(t, err)
	require.Len(t, links, 4)

	byID := make(map[int]audio.LinkInfo)
	for _, l := range links {
		byID[l.ID] = l
	}

	// Speaker A links: both active
	a1 := byID[101]
	assert.Equal(t, 99, a1.OutputNodeID)
	assert.Equal(t, 42, a1.InputNodeID)
	assert.Equal(t, "active", a1.State)

	a2 := byID[102]
	assert.Equal(t, "active", a2.State)

	// Speaker B links: both paused (zombie state)
	b1 := byID[103]
	assert.Equal(t, 99, b1.OutputNodeID)
	assert.Equal(t, 77, b1.InputNodeID)
	assert.Equal(t, "paused", b1.State)

	b2 := byID[104]
	assert.Equal(t, "paused", b2.State)
}

func TestParseLinks_EmptyInput(t *testing.T) {
	links, err := audio.ParseLinks([]byte("[]"))
	require.NoError(t, err)
	assert.Empty(t, links)
}

func TestParseLinks_InvalidJSON(t *testing.T) {
	_, err := audio.ParseLinks([]byte("{bad json"))
	require.Error(t, err)
}

func TestParseNodesByName(t *testing.T) {
	data, err := os.ReadFile("testdata/pw-dump.json")
	require.NoError(t, err)

	byName, err := audio.ParseNodesByName(data)
	require.NoError(t, err)

	assert.Equal(t, 42, byName["bluez_output.AA_BB_CC_DD_EE_FF.1"])
	assert.Equal(t, 77, byName["bluez_output.11_22_33_44_55_66.1"])
	assert.Equal(t, 99, byName["rtp-source"])
	assert.Equal(t, 10, byName["alsa_output.platform-bcm2835_audio.stereo-fallback"])
}

func TestParseNodesByName_Empty(t *testing.T) {
	byName, err := audio.ParseNodesByName([]byte("[]"))
	require.NoError(t, err)
	assert.Empty(t, byName)
}

func TestParseLinks_SkipsEmptyState(t *testing.T) {
	data := []byte(`[
		{"id": 1, "type": "PipeWire:Interface:Link", "info": {"output-node-id": 10, "input-node-id": 20, "state": "active"}},
		{"id": 2, "type": "PipeWire:Interface:Link", "info": {"output-node-id": 10, "input-node-id": 20, "state": ""}},
		{"id": 3, "type": "PipeWire:Interface:Link", "info": {"output-node-id": 10, "input-node-id": 20}}
	]`)
	links, err := audio.ParseLinks(data)
	require.NoError(t, err)
	require.Len(t, links, 1, "links with empty or missing state should be skipped")
	assert.Equal(t, "active", links[0].State)
}

func TestParseLinks_IgnoresNonLinkObjects(t *testing.T) {
	data, err := os.ReadFile("testdata/pw-dump.json")
	require.NoError(t, err)

	links, err := audio.ParseLinks(data)
	require.NoError(t, err)

	// Must not include Node or Core objects, only the 4 Link objects
	for _, l := range links {
		assert.NotZero(t, l.ID)
		assert.NotEmpty(t, l.State)
	}
	assert.Len(t, links, 4)
}

func TestParseNodeByName(t *testing.T) {
	data, err := os.ReadFile("testdata/pw-dump.json")
	require.NoError(t, err)

	id, err := audio.ParseNodeByName(data, "rtp-source")
	require.NoError(t, err)
	assert.Equal(t, 99, id)

	id, err = audio.ParseNodeByName(data, "bluez_output.AA_BB_CC_DD_EE_FF.1")
	require.NoError(t, err)
	assert.Equal(t, 42, id)
}

func TestParseNodeByName_Missing(t *testing.T) {
	id, err := audio.ParseNodeByName([]byte("[]"), "nonexistent")
	require.NoError(t, err)
	assert.Equal(t, -1, id)
}

func TestParseNodeByName_InvalidJSON(t *testing.T) {
	_, err := audio.ParseNodeByName([]byte("{bad json"), "x")
	require.Error(t, err)
}

func TestParseSources_InvalidJSON(t *testing.T) {
	_, err := audio.ParseSources([]byte("{bad json"))
	require.Error(t, err)
}

func TestParseSourceNodeID_InvalidJSON(t *testing.T) {
	_, err := audio.ParseSourceNodeID([]byte("{bad json"), "rtp-source")
	require.Error(t, err)
}

func TestParseNodesByName_InvalidJSON(t *testing.T) {
	_, err := audio.ParseNodesByName([]byte("{bad json"))
	require.Error(t, err)
}
