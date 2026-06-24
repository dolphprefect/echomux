package audio

import (
	"testing"
)

func TestParseModuleID(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		want    int
		wantErr bool
	}{
		{
			name:   "clean integer with newline",
			output: "42\n",
			want:   42,
		},
		{
			name:   "trailing spaces",
			output: "42   \n",
			want:   42,
		},
		{
			name:   "leading spaces",
			output: "  42\n",
			want:   42,
		},
		{
			name:   "no trailing newline",
			output: "7",
			want:   7,
		},
		{
			name:   "warning prefix then integer",
			output: "W: [pipewire] some system notification\n42\n",
			want:   42,
		},
		{
			name:   "multiple warning lines then integer",
			output: "W: pulse compat warn\npipewire graph changed\n99\n",
			want:   99,
		},
		{
			name:   "large module ID",
			output: "99999\n",
			want:   99999,
		},
		{
			name:   "integer zero",
			output: "0\n",
			want:   0,
		},
		{
			name:    "empty output",
			output:  "",
			wantErr: true,
		},
		{
			name:    "only whitespace",
			output:  "   \n   \n",
			wantErr: true,
		},
		{
			name:    "only warning lines no integer",
			output:  "W: pipewire warning\nWARN: something else\n",
			wantErr: true,
		},
		{
			name:    "text without integer",
			output:  "some random text\nanother line\n",
			wantErr: true,
		},
		{
			name:   "integer after blank line",
			output: "\n\n17\n",
			want:   17,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseModuleID([]byte(tc.output))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got id=%d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParseOrphanRTPModules(t *testing.T) {
	output := `0 module-device-restore
14 module-rtp-send source=main-mix.monitor destination_ip=192.168.1.51 port=9001 format=s16be channels=2 rate=48000
15 module-rtp-send source=main-mix.monitor destination_ip=192.168.1.52 port=9002 format=s16be channels=2 rate=48000
16 module-rtp-send source=main-mix.monitor destination_ip=192.168.1.53 port=9001 format=s16be channels=2 rate=48000
`
	got := parseOrphanRTPModules([]byte(output), 9001)
	want := []int{14, 16}
	if len(got) != len(want) {
		t.Fatalf("got len %d, want %d", len(got), len(want))
	}
	for i, v := range got {
		if v != want[i] {
			t.Fatalf("got[%d]=%d, want %d", i, v, want[i])
		}
	}
}
