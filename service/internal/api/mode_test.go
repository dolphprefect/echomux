package api_test

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"

	"github.com/dolphprefect/echomux/internal/api"
	"github.com/dolphprefect/echomux/internal/audio"
	"github.com/dolphprefect/echomux/internal/bluetooth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ParseMode

func TestParseMode_Valid(t *testing.T) {
	cases := []struct {
		input string
		want  api.Mode
	}{
		{"standalone", api.ModeStandalone},
		{"master", api.ModeMaster},
		{"satellite", api.ModeSatellite},
	}
	for _, tc := range cases {
		got, err := api.ParseMode(tc.input)
		require.NoErrorf(t, err, "ParseMode(%q)", tc.input)
		assert.Equal(t, tc.want, got)
	}
}

func TestParseMode_Invalid(t *testing.T) {
	for _, bad := range []string{"", "MASTER", "unknown", "Master", "Standalone"} {
		_, err := api.ParseMode(bad)
		assert.Errorf(t, err, "ParseMode(%q) should return error", bad)
	}
}

// ValidateConfig

func TestValidateConfig_OK(t *testing.T) {
	cases := []struct {
		mode       api.Mode
		masterAddr string
	}{
		{api.ModeStandalone, ""},
		{api.ModeMaster, ""},
		{api.ModeSatellite, "192.168.1.50:56644"},
	}
	for _, tc := range cases {
		err := api.ValidateConfig(tc.mode, tc.masterAddr)
		assert.NoErrorf(t, err, "ValidateConfig(%v, %q)", tc.mode, tc.masterAddr)
	}
}

func TestValidateConfig_SatelliteRequiresMasterAddr(t *testing.T) {
	err := api.ValidateConfig(api.ModeSatellite, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "master-addr")
}

func TestValidateConfig_SatelliteRejectsMissingPort(t *testing.T) {
	// net.SplitHostPort rejects addresses with no port separator.
	for _, addr := range []string{"192.168.1.50", "not-an-address", "hostname-only"} {
		err := api.ValidateConfig(api.ModeSatellite, addr)
		assert.Errorf(t, err, "ValidateConfig(satellite, %q) should return error for missing port", addr)
	}
}

func TestValidateConfig_SatelliteAcceptsValidHostPort(t *testing.T) {
	for _, addr := range []string{"192.168.1.50:56644", "myhost:56644", "[::1]:56644"} {
		err := api.ValidateConfig(api.ModeSatellite, addr)
		assert.NoErrorf(t, err, "ValidateConfig(satellite, %q) should accept valid host:port", addr)
	}
}

func TestValidateConfig_NonSatelliteMasterAddrIsAllowed(t *testing.T) {
	// master-addr on non-satellite is accepted without error (may be ignored by callers).
	assert.NoError(t, api.ValidateConfig(api.ModeStandalone, "192.168.1.50:56644"))
	assert.NoError(t, api.ValidateConfig(api.ModeMaster, "192.168.1.50:56644"))
}

// With* options applied to NewServer

func newServerWithMode(t *testing.T, mode api.Mode, name, masterAddr string) *httptest.Server {
	t.Helper()
	btMgr := bluetooth.NewMockManager()
	audioCtr := audio.NewMockController(nil)
	noop := func(nodeName string, _ int) *exec.Cmd { return exec.Command("true") }
	srv := api.NewServer(btMgr, audioCtr,
		api.WithSpawn(noop),
		api.WithMode(mode),
		api.WithName(name),
		api.WithMasterAddr(masterAddr),
	)
	return httptest.NewServer(srv)
}

func TestWithMode_StandaloneDoesNotBreakGetDevices(t *testing.T) {
	ts := newServerWithMode(t, api.ModeStandalone, "home", "")
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestWithMode_MasterDoesNotBreakGetDevices(t *testing.T) {
	ts := newServerWithMode(t, api.ModeMaster, "living-room", "")
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestWithMode_SatelliteDoesNotBreakGetDevices(t *testing.T) {
	ts := newServerWithMode(t, api.ModeSatellite, "kitchen", "192.168.1.50:56644")
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/devices")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
