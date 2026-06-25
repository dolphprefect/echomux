package audio

// NOTE: The RTP sink implementation was migrated from pactl (module-rtp-send via
// pipewire-pulse) to a native pw-cli persistent-subprocess approach. pactl was found
// to crash pipewire-pulse on connection (libpulse 17.0 / PipeWire 1.4.2 protocol
// mismatch). The module lifecycle is now tied to a pw-cli process: load on satellite
// connect, kill process on disconnect. This is a deliberate behaviour change.

import (
	"strings"
	"testing"
)

func TestRTPSinkPayload(t *testing.T) {
	cases := []struct {
		destIP string
		port   int
		checks []string
	}{
		{
			destIP: "192.168.1.2",
			port:   9001,
			checks: []string{
				"libpipewire-module-rtp-sink",
				`"destination.ip":"192.168.1.2"`,
				`"destination.port":9001`,
				`"audio.format":"S16BE"`,
				`"audio.channels":2`,
				`"audio.rate":48000`,
				`"source.name":"rtp-sink"`,
				`"target.object":"main-mix-source"`,
			},
		},
		{
			destIP: "10.0.0.5",
			port:   9002,
			checks: []string{
				`"destination.ip":"10.0.0.5"`,
				`"destination.port":9002`,
			},
		},
	}

	for _, tc := range cases {
		got := rtpSinkPayload(tc.destIP, tc.port)
		for _, want := range tc.checks {
			if !strings.Contains(got, want) {
				t.Errorf("rtpSinkPayload(%q, %d): missing %q in %q", tc.destIP, tc.port, want, got)
			}
		}
	}
}

func TestParseModuleRef(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{
			name:   "simple ref",
			output: "1 = @module:22\n",
			want:   "@module:22",
		},
		{
			name:   "with prompt prefix",
			output: ">> load-module libpipewire-module-rtp-sink ...\n1 = @module:5\n",
			want:   "@module:5",
		},
		{
			name:   "large module number",
			output: "1 = @module:9999\n",
			want:   "@module:9999",
		},
		{
			name:   "with surrounding noise",
			output: "remote 0 is named 'pipewire-0'\n1 = @module:3\nError: \"unsupported type\"\n",
			want:   "@module:3",
		},
		{
			name:    "no module ref",
			output:  "Error: module not found\n",
			wantErr: true,
		},
		{
			name:    "empty output",
			output:  "",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseModuleRef([]byte(tc.output))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got ref=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
