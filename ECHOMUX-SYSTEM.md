# echomux — System Architecture & Technical Reference

This document covers how echomux works internally, the PipeWire and BlueZ configuration it depends on, troubleshooting procedures, and how to build and deploy.

For the HTTP API see [ECHOMUX-API.md](ECHOMUX-API.md). For the end-user overview see [README.md](README.md).

---

## Operating modes

echomux runs in one of three modes set via `ECHOMUX_MODE`:

| Mode | Description |
|---|---|
| `standalone` | Default. Single node; no satellite support |
| `master` | Accepts satellite connections; proxies HTTP to satellites; aggregates devices in `GET /devices` |
| `satellite` | Connects to master via WebSocket; receives audio via RTP unicast; exposes local REST API |

---

## Single-node architecture

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
- Each loopback runs: `pw-loopback --capture main-mix-source --playback <node-name> --latency 200 --delay <seconds>`
- Per-speaker delay is implemented by passing `--delay` to pw-loopback; changing delay kills and respawns the loopback (the only way to adjust delay at runtime)
- Volume and mute are applied via **wpctl** to the PipeWire node

---

## Multi-node / satellite architecture

```
                        MASTER Pi
  ┌─────────────────────────────────────────────────────┐
  │  Spotify → librespot → pw-cat → main-mix             │
  │                                                      │
  │  echomux binary (mode=master)                        │
  │  ┌──────────────┐  ┌──────────────────────────────┐ │
  │  │  web UI      │  │  /nodes WebSocket server     │ │
  │  │  REST API    │  │  satellite registry          │ │
  │  │  /nodes/{id} │  │  RTP sink manager            │ │
  │  │  HTTP proxy  │  └──────────────────────────────┘ │
  └──────────────────────────┬──────────────────────────┘
                             │
              ┌──────────────┴──────────────┐
              │ WebSocket (/nodes)          │ WebSocket (/nodes)
              │ RTP unicast (UDP 9001)      │ RTP unicast (UDP 9001)
              ▼                             ▼
        SATELLITE Pi (bedroom)       SATELLITE Pi (kitchen)
        ┌─────────────────────┐     ┌─────────────────────┐
        │  rtp-source (PW)    │     │  rtp-source (PW)    │
        │  main-mix           │     │  main-mix           │
        │  pw-loopbacks       │     │  pw-loopbacks       │
        │  BT speakers        │     │  BT speakers        │
        │  echomux REST API   │     │  echomux REST API   │
        └─────────────────────┘     └─────────────────────┘
```

### Control plane (WebSocket `/nodes`)

Each satellite dials `ws://<master>:56644/nodes` on startup and sends a `register` message with its name and public address. The master:

1. Derives a node ID by slugifying the satellite name (spaces → hyphens, lowercased)
2. Checks for a duplicate: if a node with the same ID is already online, the new connection is rejected with `StatusPolicyViolation`. If the node exists but is offline (stale session), its stale RTP sink is removed and the new session takes over
3. Resolves the satellite's public IP: if the satellite reports an empty or unroutable address, the master uses the TCP connection's remote IP
4. Spawns a `pw-cli` subprocess that loads `libpipewire-module-rtp-sink` pointing at the satellite's IP and port
5. Sends `{"type":"registered","id":"<node-id>"}` back to the satellite
6. Broadcasts `satellite_online` to all `/events` WebSocket clients

The connection is kept alive with a **ping/pong heartbeat** every 10 seconds (5-second pong timeout). A missed pong closes the connection.

**Device state sync** uses two mechanisms:
- **Delta events**: the satellite sends `{"type":"event", "mac":"...", "event":"connected|disconnected", "seq":N}` for each BT state change. The master applies these to its cached device list. Each delta carries a monotonically increasing `seq`; if the master detects a gap it sends `{"type":"request_sync"}` to request a full push
- **Full push**: the satellite responds to `request_sync` with `{"type":"devices", "devices":[...]}` — a complete snapshot. The master also sends `request_sync` on a 5-minute timer as a backstop. On the satellite side, a full push is also triggered whenever speaker state changes (volume, mute, delay, forget)

When the connection drops, the master kills the RTP sink subprocess, marks the node offline, clears its device cache, and broadcasts `satellite_offline`.

**Satellite reconnect backoff:** starts at 1 s, doubles on each failed attempt (capped at 30 s), with ±15% jitter to prevent thundering-herd reconnects after a shared power outage. The backoff resets to zero after any session that successfully completed registration.

Both ends set a **512 KiB WebSocket read limit** immediately after the connection is established.

### Audio plane (RTP unicast)

Audio flows from master to satellite as raw PCM wrapped in RTP (S16BE / 48000 Hz / 2 channels):

```
main-mix (PipeWire on master)
   └──▶ libpipewire-module-rtp-sink (UDP unicast to satellite IP:9001)
             └──▶ libpipewire-module-rtp-source (PipeWire on satellite)
                       └──▶ main-mix (satellite)
                                 └──▶ pw-loopbacks → BT speakers
```

**RTP sink (master side):** managed by a persistent `pw-cli` subprocess per satellite session. The subprocess holds the PipeWire module loaded; killing it unloads the module. `pw-cli` is started with `Pdeathsig: SIGKILL` so it dies automatically if echomux crashes. At master startup, any surviving `pw-cli` processes from a previous crash are killed with `pkill -KILL -x pw-cli` before new sinks are created.

**RTP source (satellite side):** loaded dynamically by the satellite before each connection attempt to the master, also via a persistent `pw-cli` subprocess. The subprocess is killed and respawned fresh on every reconnect — the PipeWire module session does not auto-recover when the RTP stream restarts with a new SSRC after a master restart. The `10-rtp-source.conf` file installed to `/etc/pipewire/pipewire.conf.d/` on the satellite is intentionally empty; the module is loaded entirely by echomux at runtime.

The `rtp-source → main-mix` link is managed by **tickRouter** on the satellite, which creates the link if it is missing on every 2-second tick.

### HTTP proxy (`/nodes/{id}/...`)

The master proxies all REST calls to satellites:

- `POST /nodes/bedroom/scan` → `POST http://192.168.1.10:56644/scan`
- The master uses an `http.Transport` with `ResponseHeaderTimeout: 5s`
- Scan responses flush their `200 OK` headers immediately before the scan runs, so the proxy's short header timeout is not triggered by a 10-second scan

The proxy only knows satellite nodes. Calling `/nodes/{masterNodeId}/...` returns `404 node_not_found`. UI code must convert the master's node ID to `undefined` (via `nodeApiId()` in `App.svelte`) before routing API calls.

---

## Go service internals

The `echomux` binary (`service/cmd/echomux/main.go`) runs several concurrent loops:

### tickRouter (every 2 s)

1. Calls `pw-dump` to snapshot the PipeWire graph
2. Ensures the `rtp-source → main-mix` link exists (creates it if missing) — applies on satellite only; no-op on standalone/master where there is no rtp-source
3. For every `bluez_output.*` BT sink node:
   - Registers the MAC as a known speaker if not already known
   - Spawns a pw-loopback if none is running
   - Restarts the loopback if the PipeWire node name changed (e.g. `.1 → .2` after reconnect)
4. **Zombie watchdog**: if a loopback has been running >5 s but its PipeWire link is not in `active` state, kills and restarts it. A 30 s cooldown prevents thrash
5. Kills loopbacks for speakers whose BT node has disappeared
6. Is a no-op while `paused = true` (set during BT scanning)

### hub (WebSocket `/events`)

Reads BlueZ D-Bus events (connected / disconnected / paired) and forwards them to all `/events` WebSocket clients. Also sends `loopback_started` / `loopback_stopped` events when tickRouter spawns or kills a loopback. In master mode, also forwards BT events from satellites (received via the control plane) and emits `satellite_online` / `satellite_offline`.

The hub also has an in-process `Subscribe` mechanism used by the satellite client to capture local BT events for forwarding to the master.

### startupConnect

After the server starts, a background goroutine attempts to reconnect speakers that were live at the last save (the `connected_speakers` set in the state file).

### handleConnect

Retries the BlueZ connect call up to 3 times with a 2-second delay between attempts. `AlreadyConnected` errors are treated as success. `DeviceNotFoundError` is not retried. After a successful connect, a goroutine polls tickRouter at 500ms intervals for up to 30 seconds to ensure the loopback starts.

### handleDelay

Stores the new delay and, if a loopback is already running for the speaker, kills and immediately restarts it with the new `--delay` value.

### handleRestart

Kills all tracked loopbacks (without disconnecting BT), clears watchdog cooldowns, and calls `killOrphanLoopbacks()` to kill any untracked `pw-loopback` processes. tickRouter restarts the loopbacks within 2 seconds.

### handleScan

Pauses tickRouter, disconnects all active BT speakers (to free the radio), flushes the HTTP response headers before starting the scan (so proxies don't time out), and runs a BlueZ discovery for the requested duration (default 10 s). If the client disconnects before calling `POST /playback/resume`, an `AfterFunc` callback auto-unpauses.

### State persistence

Speaker settings (volumes, mutes, delays, known speaker list, last-connected set) are saved to a JSON file. Writes are debounced 200 ms and use an atomic rename (`write to .tmp → rename`) to prevent partial reads.

Default path: `~/.local/share/echomux/state.json`

```json
{
  "delays":             {"C8:24:78:67:83:C0": 250},
  "volumes":            {"C8:24:78:67:83:C0": 74},
  "mutes":              {},
  "known_speakers":     {"C8:24:78:67:83:C0": true},
  "connected_speakers": {"C8:24:78:67:83:C0": true}
}
```

`known_speakers` is populated the first time a device's `bluez_output.*` PipeWire node appears. This is how echomux distinguishes A2DP speakers from phones, keyboards, and other Bluetooth devices.

`connected_speakers` is the set of MACs that had live loopbacks at the last save; used by `startupConnect` to reconnect speakers after a service restart.

---

## PipeWire configuration

Custom config files installed to `/etc/pipewire/pipewire.conf.d/`:

| File | Purpose |
|---|---|
| `10-rtp-source.conf` | Intentionally empty on satellite. The `rtp-source` module is loaded dynamically by echomux via `pw-cli` before each connection attempt to the master. A static module here would hold stale PipeWire session state across master restarts |
| `20-main-mix-loopback.conf` | Creates the `main-mix` virtual sink that aggregates audio before fanning out to speakers |

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

echomux requires **BlueZ 5.83 or later**. BlueZ ≤ 5.82 cannot maintain simultaneous A2DP connections to more than one sink device (fixed in commit `05f8bd4`). The install script builds BlueZ from source.

---

## go-bluetooth vendor patches

`github.com/muka/go-bluetooth` is vendored at `service/vendor/`. Two patches are applied:

**1. `util/map_struct.go`** — `MapToStruct` skips fields it cannot decode instead of returning an error. Required for devices that expose `AdvertisingData` with `uint8` keys (common on generic BT speakers), which would otherwise cause `GetDevices()` to fail for those devices.

**2. `bluez/profile/adapter/adapter_devices.go`** — `parseDevice` ignores `MapToStruct` errors (belt-and-suspenders with patch 1).

---

## systemd unit

The service unit (`service/echomux.service`) requires:

```
After=bluetooth.service pipewire-system.service wireplumber-system.service pipewire-pulse-system.socket
Wants=pipewire-system.service wireplumber-system.service
Requires=pipewire-pulse-system.socket
```

`Requires=pipewire-pulse-system.socket` ensures the PipeWire-Pulse socket is available before echomux starts, preventing `pactl` connection-refused errors on fast boot.

The unit reads environment from `/etc/echomux/echomux.conf` and sets `PIPEWIRE_RUNTIME_DIR=/run/pipewire` for all child processes.

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
make build

# Deploy to master Pi (local)
make deploy-master   # build + stop service + install binary + start service

# Deploy to satellite Pi (requires Makefile.local with SSH target)
make deploy-satellite

# Deploy to both
make deploy-all
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
| `make ui` | Build the Svelte frontend → `service/internal/api/static/` (runs `npm install` first via Makefile.local) |
| `make build` | Build UI, then compile Go binary |
| `make install` | Build, stop service, install binary, start service (local Pi) |
| `make deploy` | Alias for `make install` |
| `make deploy-master` | Build and deploy to the master Pi (defined in `Makefile.local`) |
| `make deploy-satellite` | Build and deploy to the satellite Pi via SSH (defined in `Makefile.local`) |
| `make deploy-all` | Build and deploy to both master and satellite in one shot (defined in `Makefile.local`) |

### Satellite configuration file

Copy to `/etc/echomux/echomux.conf` on the satellite Pi and adjust:

```ini
ECHOMUX_ADAPTER=hci0        # adapter to use (hci0, hci1, etc.)
ECHOMUX_ADDR=:56644
ECHOMUX_MODE=satellite
ECHOMUX_NAME=bedroom        # label shown in the UI; slugified to derive the node ID
ECHOMUX_MASTER_ADDR=192.168.1.3:56644
ECHOMUX_SELF_ADDR=192.168.1.X:56644  # this satellite's public IP:port for master HTTP proxy
ECHOMUX_DEBUG=true
```

`ECHOMUX_SELF_ADDR` is the satellite's public address reported to the master for HTTP proxying. If omitted or set to an unroutable address (0.0.0.0), the master falls back to the TCP connection's remote IP.

---

## Repo layout

```
echomux/
├── service/
│   ├── cmd/echomux/main.go          — binary entry point, flag parsing
│   ├── internal/
│   │   ├── api/                     — HTTP handlers, tickRouter, WebSocket hub
│   │   │   ├── handlers.go          — all REST handlers + statusWriter middleware
│   │   │   ├── satellite_server.go  — /nodes WebSocket server, satellite registry, RTP sink lifecycle
│   │   │   ├── satellite_client.go  — satellite-side WS client, reconnect loop, event forwarder
│   │   │   ├── noderegistry.go      — in-memory satellite registry with seq-based delta tracking
│   │   │   ├── events.go            — WebSocket hub (broadcast + Subscribe)
│   │   │   ├── proxy.go             — /nodes/{id}/... reverse proxy to satellites
│   │   │   └── static/              — embedded web UI (built from service/ui/)
│   │   ├── audio/                   — PipeWire shell wrappers (pw-dump, wpctl, pw-link, rtp.go)
│   │   └── bluetooth/               — BlueZ D-Bus wrapper (go-bluetooth)
│   ├── ui/                          — Svelte frontend source
│   │   └── src/
│   │       ├── App.svelte           — main component (node list, speaker list, WebSocket, polling)
│   │       ├── lib/api.js           — fetch wrapper; prepends /nodes/{id} when nodeId is truthy
│   │       └── components/
│   │           ├── NodeSection.svelte — per-node group heading bar + speaker list
│   │           ├── DeviceCard.svelte  — per-speaker card (volume, mute, delay chip)
│   │           ├── ScanSheet.svelte   — BT scan and pair flow (node-aware)
│   │           └── DelaySheet.svelte  — per-speaker delay adjustment (accepts nodeId prop)
│   ├── setup/
│   │   ├── install.sh               — full bootstrap script
│   │   ├── echomux.service          — systemd unit
│   │   ├── echomux-satellite.conf   — satellite configuration template
│   │   ├── librespot.service        — librespot systemd unit
│   │   ├── librespot-pipewire.sh    — librespot → pw-cat wrapper script
│   │   ├── echomux-avahi.service    — mDNS advertisement
│   │   ├── 50-systemwide.conf       — WirePlumber headless config
│   │   ├── 51-bluetooth-roles.conf  — WirePlumber BT role config
│   │   ├── 52-bt-isolation.conf     — WirePlumber BT latency buffer
│   │   ├── 20-main-mix-loopback.conf — PipeWire main-mix virtual sink
│   │   └── 10-rtp-source.conf       — intentionally empty; rtp-source loaded dynamically by echomux
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
curl -s -X POST http://localhost:56644/playback/restart
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

### Satellite not appearing in the UI

```bash
# On the satellite:
journalctl -u echomux -f   # look for "registered with master" or connection errors

# On the master:
journalctl -u echomux -f   # look for "satellite registered" or "satellite offline"

# Verify the satellite can reach the master
curl http://192.168.1.3:56644/nodes
```

### Satellite discovers no Bluetooth devices

Common on USB Bluetooth dongles with outdated firmware. Symptoms: `Opcode 0x2042 failed: -16` in `dmesg` (HCI_OP_LE_SET_SCAN_PARAMETERS returns -EBUSY).

```bash
# Check firmware version on the satellite
ls -la /lib/firmware/rtl_bt/

# Reload the BT driver
sudo rmmod btusb && sudo modprobe btusb

# If a known-good firmware exists on another Pi, copy it:
scp /lib/firmware/rtl_bt/rtl8761bu_fw.bin pi@<satellite-ip>:/tmp/
# then on satellite: sudo cp /tmp/rtl8761bu_fw.bin /lib/firmware/rtl_bt/
# then: sudo rmmod btusb && sudo modprobe btusb
```

### RTP sink orphans after master crash

```bash
# Kill any orphaned pw-cli processes (echomux does this automatically at startup)
sudo pkill -KILL -x pw-cli
sudo systemctl restart echomux
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
