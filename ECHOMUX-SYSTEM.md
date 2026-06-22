# echomux — System Architecture & Technical Reference

This document covers how echomux works internally, the PipeWire and BlueZ configuration it depends on, troubleshooting procedures, and how to build and deploy.

For the HTTP API see [ECHOMUX-API.md](ECHOMUX-API.md). For the end-user overview see [README.md](README.md).

---

## How it works

```
Spotify app (phone)
      │  Spotify Connect (Wi-Fi)
      ▼
 librespot  ──raw PCM──▶  pw-cat ──▶  main-mix  (PipeWire virtual sink)
                                           │
                    ┌──────────────────────┼──────────────────────┐
                    ▼                      ▼                      ▼
              pw-loopback           pw-loopback           pw-loopback
                    │                      │                      │
             bluez_output.*         bluez_output.*         bluez_output.*
                    │                      │                      │
              Speaker 1              Speaker 2              Speaker N
```

- **librespot** receives audio from Spotify Connect over Wi-Fi and pipes raw PCM to **pw-cat**
- **pw-cat** writes the audio into **main-mix**, a PipeWire virtual sink
- The Go service watches the PipeWire graph every 2 seconds (**tickRouter**). When a Bluetooth A2DP speaker is connected, it spawns a **pw-loopback** process that reads from `main-mix-source` and writes to that speaker's `bluez_output.*` node
- Each loopback process is independent. Per-speaker delay is implemented by passing `--delay` to pw-loopback; changing delay kills and respawns the loopback (the only way to adjust delay at runtime)
- Volume and mute are applied via **wpctl** to the PipeWire node

---

## Go service internals

The `echomux` binary (`service/cmd/echomux/main.go`) runs two main loops:

### tickRouter (every 2 s)

1. Calls `pw-dump` to snapshot the PipeWire graph
2. Ensures the `rtp-source → main-mix` link exists (creates it if missing)
3. For every `bluez_output.*` BT sink node:
   - Registers the MAC as a known speaker if not already known
   - Spawns a pw-loopback if none is running
   - Restarts the loopback if the PipeWire node name changed (e.g. `.1 → .2` after reconnect)
4. **Zombie watchdog**: if a loopback has been running >5 s but its PipeWire link is not in `active` state, kills and restarts it. A 30 s cooldown prevents thrash
5. Kills loopbacks for speakers whose BT node has disappeared

### hub (WebSocket)

Reads BlueZ D-Bus events (connected / disconnected / paired) and forwards them to all WebSocket clients. Also sends `loopback_started` / `loopback_stopped` events when the tickRouter spawns or kills a loopback.

### State persistence

Speaker settings (volumes, mutes, delays, known speaker list) are saved to a JSON file. Writes are debounced 200 ms and use an atomic rename to prevent partial reads.

Default path: `~/.local/share/echomux/state.json`

```json
{
  "delays":         {"C8:24:78:67:83:C0": 250},
  "volumes":        {"C8:24:78:67:83:C0": 74},
  "mutes":          {},
  "known_speakers": {"C8:24:78:67:83:C0": true}
}
```

`known_speakers` is populated the first time a device's `bluez_output.*` PipeWire node appears. This is how echomux distinguishes A2DP speakers from phones, keyboards, and other Bluetooth devices.

---

## PipeWire configuration

Custom config files installed to `/etc/pipewire/pipewire.conf.d/`:

| File | Purpose |
|---|---|
| `10-rtp-source.conf` | Creates the `rtp-source` node — receives raw PCM from librespot via UDP on port 9001 |
| `20-main-mix-loopback.conf` | Creates the `main-mix` virtual sink that aggregates audio from librespot before fanning out to speakers |

### Why main-mix

Without main-mix, pw-cat would route directly to the highest-priority BT sink. By routing through main-mix we can fan out to N sinks via independent loopbacks without WirePlumber's stream-routing policy interfering.

### Why node.dont-move

WirePlumber's `linking.allow-moving-streams` policy would move `pw-cat` to the highest-priority BT sink when a speaker connects, overriding `--target main-mix`. Setting `PIPEWIRE_PROPS={"node.dont-move":true}` on the pw-cat node prevents this.

---

## WirePlumber configuration

Custom files installed to `/etc/wireplumber/wireplumber.conf.d/`:

| File | Purpose |
|---|---|
| `50-systemwide.conf` | Disables logind seat monitoring (required for headless / system-level PipeWire) |
| `51-bluetooth-roles.conf` | Enables A2DP source role; disables A2DP sink (prevents the Pi from appearing as a speaker to other devices) |
| `52-bt-isolation.conf` | Sets `node.latency = 8192/48000` (~170 ms) on all BT output nodes to absorb Bluetooth retransmit jitter |

---

## BlueZ requirements

echomux requires **BlueZ 5.83 or later**. BlueZ ≤ 5.82 cannot maintain simultaneous A2DP connections to more than one sink device (fixed in commit `05f8bd4`). The install script pins BlueZ from Debian Sid.

---

## go-bluetooth vendor patches

`github.com/muka/go-bluetooth` is vendored at `service/vendor/`. Two patches are applied:

**1. `util/map_struct.go`** — `MapToStruct` skips fields it cannot decode instead of returning an error. Required for devices that expose `AdvertisingData` with `uint8` keys (common on generic BT speakers), which would otherwise cause `GetDevices()` to fail for those devices.

**2. `bluez/profile/adapter/adapter_devices.go`** — `parseDevice` ignores `MapToStruct` errors (belt-and-suspenders with patch 1).

---

## Build & deploy

```bash
# Go backend tests
cd service/
go test ./...

# UI tests
cd service/ui/
npm test

# Build UI then compile Go binary
cd service/
make build

# Deploy to running Pi
make install   # stops echomux, copies binary, starts echomux
```

Manual deploy:

```bash
cd service/
go build -o /tmp/echomux ./cmd/echomux
sudo systemctl stop echomux
sudo cp /tmp/echomux /usr/local/bin/echomux
sudo systemctl start echomux
```

### Makefile targets

| Target | Description |
|---|---|
| `make ui` | Build the Svelte frontend → `service/internal/api/static/` |
| `make build` | Build UI, then compile Go binary |
| `make install` | Build, stop service, install binary, start service |

---

## Repo layout

```
echomux/
├── service/
│   ├── cmd/echomux/main.go          — binary entry point, flag parsing
│   ├── internal/
│   │   ├── api/                     — HTTP handlers, tickRouter, WebSocket hub
│   │   │   └── static/              — embedded web UI (built from service/ui/)
│   │   ├── audio/                   — PipeWire shell wrappers (pw-dump, wpctl, pw-link)
│   │   └── bluetooth/               — BlueZ D-Bus wrapper (go-bluetooth)
│   ├── ui/                          — Svelte frontend source
│   │   └── src/
│   │       ├── App.svelte           — main component (speaker list, WebSocket, polling)
│   │       ├── lib/api.js           — fetch wrapper
│   │       └── components/
│   │           ├── DeviceCard.svelte — per-speaker card (volume, mute, delay chip)
│   │           ├── ScanSheet.svelte  — BT scan and pair flow
│   │           └── DelaySheet.svelte — per-speaker delay adjustment
│   ├── setup/
│   │   ├── install.sh               — full bootstrap script
│   │   ├── echomux.service          — systemd unit
│   │   ├── librespot.service        — librespot systemd unit
│   │   ├── librespot-pipewire.sh    — librespot → pw-cat wrapper script
│   │   ├── echomux-avahi.service    — mDNS advertisement
│   │   ├── 50-systemwide.conf       — WirePlumber headless config
│   │   ├── 51-bluetooth-roles.conf  — WirePlumber BT role config
│   │   ├── 52-bt-isolation.conf     — WirePlumber BT latency buffer
│   │   ├── 20-main-mix-loopback.conf — PipeWire main-mix virtual sink
│   │   └── 10-rtp-source.conf       — PipeWire RTP source (UDP 9001)
│   └── vendor/                      — vendored Go deps (go-bluetooth, patched)
├── Makefile
├── README.md
├── ECHOMUX-SYSTEM.md
├── ECHOMUX-API.md
└── ECHOMUX-APP-SPEC.md
```

---

## Troubleshooting

### No audio from any speaker

```bash
# Check that pw-cat is routing to main-mix (not directly to a BT sink)
PIPEWIRE_RUNTIME_DIR=/run/pipewire pw-dump 2>/dev/null | python3 -c "
import json,sys
data=json.load(sys.stdin)
links=[n for n in data if n.get('type')=='PipeWire:Interface:Link']
nodes={n['id']:n.get('info',{}).get('props',{}).get('node.name','?') for n in data if n.get('type')=='PipeWire:Interface:Node'}
for l in links:
    i=l.get('info',{})
    print(nodes.get(i.get('output-node-id','?'),'?'),'→',nodes.get(i.get('input-node-id','?'),'?'))
"
# Expected: pw-cat → main-mix   (NOT pw-cat → bluez_output.*)

# If pw-cat is routed wrong, restart librespot
sudo systemctl restart librespot
```

### Speaker connected but silent

The BT connection is up but the pw-loopback may not have started yet. Wait 2–4 seconds (tickRouter runs every 2 s), or force a respawn via the ↺ button in the web UI, or:

```bash
curl -s -X POST http://localhost:56644/playback/resume
```

### Service stuck paused (music won't start after scanning)

```bash
curl -s -X POST http://localhost:56644/playback/resume
```

### Audio graph in a bad state

```bash
sudo systemctl restart pipewire-system wireplumber-system
sleep 3
sudo systemctl restart librespot echomux
```

### BT adapter in a bad state

```bash
sudo systemctl restart bluetooth
# or for a specific adapter:
sudo hciconfig hci0 down && sudo hciconfig hci0 up
```

### PipeWire graph inspection

```bash
# All nodes
PIPEWIRE_RUNTIME_DIR=/run/pipewire pw-dump | python3 -c "
import json,sys
for n in json.load(sys.stdin):
    if n.get('type')!='PipeWire:Interface:Node': continue
    p=n.get('info',{}).get('props',{})
    print(n['id'], p.get('media.class',''), p.get('node.name',''))
"

# All active links
PIPEWIRE_RUNTIME_DIR=/run/pipewire pw-link --list

# Volume tree
PIPEWIRE_RUNTIME_DIR=/run/pipewire wpctl status
```
