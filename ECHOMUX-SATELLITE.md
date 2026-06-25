# echomux Master-Satellite Architecture — Refined Specification

## Context

Bluetooth Classic range is ~10 m through walls. A single Pi can only serve speakers in its immediate vicinity. The satellite architecture lets additional Pis sit close to remote speakers while sharing a single Spotify Connect endpoint and unified web UI on the Master. A single monolithic binary handles both roles, controlled by runtime flags.

**Design decisions:**
- **Audio transport:** RTP unicast via `libpipewire-module-rtp-sink`, loaded by a persistent `pw-cli` subprocess on the master (one per satellite). The satellite's `rtp-source` is loaded dynamically by echomux (also via `pw-cli`) before each master session — the static config `10-rtp-source.conf` is intentionally empty to prevent stale session state after master restarts.
- **Discovery:** `--master-addr` flag. Satellite dials Master on startup. No Avahi browser on Master.
- **Naming:** `--name` / `ECHOMUX_NAME`. Displayed in UI section headers. Not UI-editable.

---

## Topology

```
Spotify app
     │ Spotify Connect
     ▼
librespot ──PCM──▶ main-mix (Master PipeWire)
                       │
           ┌───────────┴──────────────┐
           ▼                          ▼
     pw-loopback          rtp-sink (pw-cli) ──UDP:9001──▶ Satellite PipeWire
           │                                                       │
    bluez_output.*                                            rtp-source (pw-cli)
  (Master BT speakers)                                             │
                                                             main-mix
                                                                   │
                                                           pw-loopback
                                                                   │
                                                          bluez_output.*
                                                      (Satellite BT speakers)
```

---

## Modes & Configuration

### New flags

| Flag | Env var | Default | Notes |
|---|---|---|---|
| `--mode` | `ECHOMUX_MODE` | `standalone` | `standalone` \| `master` \| `satellite` |
| `--name` | `ECHOMUX_NAME` | hostname | Displayed in UI section headers |
| `--master-addr` | `ECHOMUX_MASTER_ADDR` | `` | Satellite only; `host:port` of Master |

Standalone mode is fully backwards-compatible — no behavior change.

### echomux.conf examples

**Master:**
```bash
ECHOMUX_MODE=master
ECHOMUX_NAME=Living Room
```

**Satellite:**
```bash
ECHOMUX_MODE=satellite
ECHOMUX_NAME=Kitchen
ECHOMUX_MASTER_ADDR=192.168.1.50:56644
```

---

## Control Plane: Satellite ↔ Master WebSocket

Master exposes `/nodes` as a WebSocket endpoint (distinct from client-facing `/events`). Satellites connect on startup, register, and maintain a heartbeat.

### Messages

**Satellite → Master:**
```json
{ "type": "register", "name": "Kitchen", "addr": "192.168.1.51:56644" }
{ "type": "devices",  "devices": [...] }
{ "type": "event",    "mac": "C8:24:78:67:83:C0", "event": "connected" }
{ "type": "pong" }
```

**Master → Satellite:**
```json
{ "type": "registered", "id": "kitchen" }
{ "type": "ping" }
```

All device commands (volume, scan, connect, delay) flow via the HTTP proxy — **not** this channel.

**Startup sequence for device sync:** Satellite sends `register`, receives `registered`, then immediately sends `devices` with its full current device list. Master stores this in `nodeInfo.Devices`. From that point on, `connected`/`disconnected` events are delta updates.

**Self-healing against delta drift:** A dropped WebSocket frame or missed BlueZ D-Bus signal can permanently desynchronize the Master's cache without either side knowing. Two mitigations are applied in concert:
- Satellite appends an incrementing `seq` integer to every delta event. Master detects a gap (non-consecutive seq) and sends `{ "type": "request_sync" }` down the control channel.
- Satellite broadcasts a full `devices` push on a 5-minute timer regardless, as a background consistency backstop.

```json
{ "type": "request_sync" }             ← Master → Satellite
{ "type": "devices", "devices": [...] } ← Satellite → Master (full re-sync)
```

**Sequence reset on registration:** When the Master receives a `register` message it must unconditionally reset the expected sequence counter for that node to `0`, regardless of any previously cached value. A satellite that reboots (hard power loss, `systemctl restart`) re-enters at `seq=0`; comparing that against a high-indexed cache from the previous session would trigger a false `request_sync` storm. The `register` message is the absolute sequence anchor.

**Heartbeat:** Master sends `ping` every 10 s; expects `pong` within 5 s or closes and marks offline.

**On Satellite reconnect (dirty reconnect guard):** The Master registration handler checks for an existing `nodeInfo` entry with the same name *before* allocating any new structures. If one is found, the handler must:
1. Set `nodeInfo.Online = false` and `nodeInfo.Devices = nil` — invalidates the cache immediately so any concurrent `GET /devices` call cannot read stale speaker states for a connection being torn down.
2. Call `nodeInfo.cancelConn()` — cancels the stale WS session context immediately (fast, no I/O).
3. Capture `nodeInfo.RTPModuleID` from the registry.
4. **Release the registry mutex.**
5. Asynchronously call `audio.RemoveRTPSink(moduleID)` outside the lock — `pactl` IPC can be slow; holding the mutex during this call would block all concurrent satellite registrations and heartbeats.
6. Re-acquire the mutex to write the new session state.

`GET /devices` must skip any `nodeInfo` entry where `Online == false` or `Devices == nil`, ensuring stale satellite data is never served during the async teardown window.

This prevents orphaned module IDs while keeping the critical section narrow (map reads/writes only).

**Duplicate name guard:** if a Satellite with the same name connects while one is already registered AND the old session is still alive (not stale), close the new connection with a descriptive close message. Names must be unique per deployment — fail visibly rather than silently deduplicating.

---

## HTTP API Changes

### New endpoints (on Master)

| Method | Path | Description |
|---|---|---|
| `GET` | `/nodes` | List all nodes (master + satellites) |
| `WS` | `/nodes` | Satellite control plane (Upgrade) |
| `ANY` | `/nodes/:id/*` | Reverse proxy to that node's HTTP API |

### `GET /nodes` response

```json
[
  { "id": "living-room", "name": "Living Room", "role": "master",    "online": true,  "addr": "" },
  { "id": "kitchen",     "name": "Kitchen",      "role": "satellite", "online": true,  "addr": "192.168.1.51:56644" },
  { "id": "attic",       "name": "Attic",         "role": "satellite", "online": false, "addr": "192.168.1.52:56644" }
]
```

### `GET /devices` with `node_id`

Each device gains a `node_id` field. `GET /devices` on the Master is an O(1) read from the in-memory device cache — **not** a fan-out HTTP call to satellites. The cache is populated and kept current via the WS control channel: Satellite pushes its full device list after registration, then pushes delta events (`connected`/`disconnected`) as they occur. This means `GET /devices` stays snappy even when a satellite has just dropped off the network dirty.

```json
[
  { "MAC": "C8:24:78:67:83:C0", "Name": "Edifier", "node_id": "living-room", ... },
  { "MAC": "AA:BB:CC:DD:EE:FF", "Name": "JBL",     "node_id": "kitchen",     ... }
]
```

### New WebSocket event types (Master → UI clients)

```json
{ "type": "satellite_online",  "node_id": "kitchen", "name": "Kitchen" }
{ "type": "satellite_offline", "node_id": "kitchen", "name": "Kitchen" }
{ "type": "connected",         "node_id": "kitchen", "mac": "AA:BB:CC:DD:EE:FF" }
```

Existing events gain an optional `node_id` field. Single-node deployments omit it — backwards compatible.

---

## Go Implementation

### New files

| File | Purpose |
|---|---|
| `internal/api/noderegistry.go` | `nodeInfo` struct, thread-safe `nodeRegistry` (id → info) |
| `internal/api/satellite_server.go` | `/nodes` WS handler: registration, heartbeat watchdog, BT event forwarding; `conn.SetReadLimit(512 KiB)` after accept; write deadline set before each outbound push |
| `internal/api/satellite_client.go` | Satellite-mode WS client: dial, register, reconnect loop with jitter, pong; `conn.SetReadLimit(512 KiB)` after dial; write deadline set before each outbound send |
| `internal/api/proxy.go` | `/nodes/:id/*` reverse proxy via `httputil.ReverseProxy`; `Director` strips `/nodes/:id` prefix; explicit `Transport` with bounded idle pool and hard timeouts; `ModifyResponse` translates satellite 5xx/timeout into HTTP 504 with `{"error":"bluetooth_subsystem","node":"kitchen"}` |
| `internal/audio/rtp.go` | `AddRTPSink` / `RemoveRTPSink` standalone functions (not via Executor — need custom env) |

### Modified files

| File | Change |
|---|---|
| `cmd/echomux/main.go` | Add `--mode`, `--name`, `--master-addr` flags; validate; pass via `WithMode`/`WithName`/`WithMasterAddr` options |
| `internal/api/handlers.go` | Add `mode`, `name`, `nodes *nodeRegistry` to `server` struct; add `WithMode`/`WithName`/`WithMasterAddr` options; extend `GET /devices` to aggregate satellite devices in parallel; add `GET /nodes` REST handler; register new routes |
| `internal/audio/interface.go` | Add `AddRTPSink(ctx, destIP, port) (moduleID int, error)` and `RemoveRTPSink(ctx, moduleID int) error` to `Controller` interface |
| `internal/api/events.go` | Add `Subscribe() (<-chan json.RawMessage, func())` fan-out for satellite client event forwarding |
| `service/setup/install.sh` | If `ECHOMUX_MODE=satellite`: skip librespot install, disable `librespot.service` |

### Key struct additions

**`nodeInfo`:**
```go
type nodeInfo struct {
    ID          string
    Name        string
    Addr        string        // host:port of satellite's HTTP API
    Online      bool
    LastSeen    time.Time
    RTPModuleID int           // pactl module ID, 0 = not loaded
    Devices     []deviceInfo  // cached device list pushed by satellite over WS
    cancelConn  context.CancelFunc // cancel func for the active WS session
}
```

**`server` struct additions:**
```go
mode       Mode            // standalone | master | satellite
name       string          // from --name flag
masterAddr string          // satellite only
nodes      *nodeRegistry   // nil in standalone/satellite mode
```

### Audio: RTP sink management

`rtp.go` implements standalone functions (not via `Executor`) that build `exec.Cmd` directly. Two env vars must be set: `PIPEWIRE_RUNTIME_DIR=/run/pipewire` (for PipeWire IPC) and `PULSE_SERVER=unix:/run/pipewire/pulse-native` (so `pactl` reaches the system-wide `pipewire-pulse` compat socket). The install script must verify `pipewire-pulse-system.service` is enabled and running on the Master, and the socket path must match what `echomux` injects into `pactl`'s environment.

```bash
# Add (returns integer module ID on stdout):
pactl load-module module-rtp-send source=main-mix.monitor \
  destination_ip=<ip> port=9001 format=s16be channels=2 rate=48000

# Remove:
pactl unload-module <ID>
```

Parser must scan line-by-line for the integer. Newer PipeWire-Pulse builds can append floating system notifications or return trailing spaces; exact index cutting will fail. Use a non-anchored regex, skip lines containing known warning substrings, and match against:

```go
var moduleIDRegex = regexp.MustCompile(`^\s*([0-9]+)\s*$`)
```

All `pactl` invocations in `rtp.go` **must** use `exec.CommandContext` with a hard 3-second deadline. If the context deadline expires the child process is actively killed (`cmd.Process.Kill()`), a system warning is logged, and the goroutine exits cleanly. A zombie PipeWire or pulse-compat daemon that never responds to IPC would otherwise cause the async teardown goroutine to hang indefinitely, leaking file descriptors and goroutine stack memory over long uptime cycles.

**Orphan cleanup on Master startup:** call `cleanOrphanRTPModules()` in `NewServer` when `mode == ModeMaster` — runs `pactl list modules short`, inspects each `module-rtp-send` line's argument string, and unloads **only** entries whose arguments contain both `port=<ECHOMUX_RTP_PORT>` and (in multicast mode) `destination_ip=<ECHOMUX_RTP_ADDRESS>`. Blindly unloading all `module-rtp-send` instances would terminate unrelated system audio infrastructure on the same host.

**Satellite loopback env parity:** The existing `defaultSpawn` in `autorouter.go` already injects `PIPEWIRE_RUNTIME_DIR=/run/pipewire` into all `pw-loopback` child processes. This must be verified to apply equally when the binary runs in satellite mode — satellite loopbacks must connect to the system PipeWire graph, not a stale user-session socket. The env injection must not be gated on `mode == ModeMaster`.

### RTP Multicast vs. Unicast

| | Unicast (current) | Multicast (preferred for N > 2) |
|---|---|---|
| Master modules | One per satellite | One global |
| Master bandwidth | O(N) | O(1) |
| Satellite config | Unchanged (`0.0.0.0:9001`) | Must join multicast group in `10-rtp-source.conf` |
| Home router support | Universal | Usually fine on LAN; rare edge cases |

**Recommendation:** Implement unicast first (simpler, no satellite config change needed). If deployments grow beyond 2 satellites, switch the Master to `destination_ip=$ECHOMUX_RTP_ADDRESS` and update `10-rtp-source.conf` on satellites to join that group — the `audio.AddRTPSink`/`RemoveRTPSink` API collapses to a one-time load on Master startup.

**Multicast loopback self-ingestion:** When using the multicast address, the network stack may echo multicast packets back to the host interface by default, causing the Master's own `rtp-source` (if any) to ingest its own outbound audio stream. The `pactl load-module module-rtp-send` invocation in multicast mode **must** append `loop=0`:
```bash
pactl load-module module-rtp-send source=main-mix.monitor \
  destination_ip=224.0.0.56 port=9001 loop=0 format=s16be channels=2 rate=48000
```
`loop=0` instructs the kernel to suppress multicast packet loopback on the sending interface.

The RTP address and port must be configurable to allow multiple independent echomux clusters on the same LAN without multicast bleed:

| Flag | Env var | Default | Notes |
|---|---|---|---|
| `--rtp-address` | `ECHOMUX_RTP_ADDRESS` | `224.0.0.56` | Multicast group (or unicast satellite IP in Phase 1) |
| `--rtp-port` | `ECHOMUX_RTP_PORT` | `9001` | Must match `10-rtp-source.conf` on satellites |

These are Master-only flags. The satellite reads its RTP port only from the static PipeWire config.

### Audio clock drift

Master and Satellite run on independent hardware clock crystals. Over long playback sessions this causes buffer underruns or overruns on the Satellite (audible as pops). PipeWire's `module-rtp-source` includes adaptive resampling that compensates for this automatically. The Satellite's `10-rtp-source.conf` should set an appropriate `latency.msec` (e.g., 200 ms) to give the resampler enough headroom. The existing `52-bt-isolation.conf` buffer (`8192/48000` ≈ 170 ms) on BT outputs absorbs downstream jitter independently. These two buffers must not overlap in a way that fights each other — configure them in series, not as competing mechanisms.

---

## UI Changes

### Single-node mode (unchanged)

When `GET /nodes` returns 1 entry, the UI renders identically to today.

### Multi-node mode layout

```
┌────────────────────────────────────────────────────┐
│ MASTER · Living Room          [+ Add]  [↻ Restart] │
├────────────────────────────────────────────────────┤
│  🔊 Edifier     [Delay: 20ms]  ████████░░  80%     │
│  🔊 Yamaha      [Delay: 65ms]  ███████░░░  75%     │
├────────────────────────────────────────────────────┤
│ SATELLITE · Kitchen           [+ Add]  [↻ Restart] │
├────────────────────────────────────────────────────┤
│  🔊 JBL Flip    [Delay:140ms]  ██████░░░░  60%     │
├────────────────────────────────────────────────────┤
│ SATELLITE · Attic             [OFFLINE]            │
└────────────────────────────────────────────────────┘
```

### New component: `NodeSection.svelte`

Props: `node` (id, name, role, online), `devices`, `connecting`, `connectErrors`

- Header: `MASTER · <name>` / `SATELLITE · <name>`
- Offline: grayed header, "OFFLINE" badge, no device cards
- `[+ Add]` → scoped `POST /nodes/:id/scan` (ScanSheet gets a `nodeId` prop)
- `[↻ Restart]` → scoped `POST /nodes/:id/playback/restart`
- Global `[↻]` in app header keeps its current behavior (Master loopbacks only)
- **Scan lockout:** when any node-scoped scan is in progress, `[+ Add]` is disabled on **all** `NodeSection` components and a spinner appears on the active node's button. A global `scanningNodeId` reactive variable in `App.svelte` drives this. This prevents parallel BlueZ scans across nodes (which share the same 2.4 GHz radio environment and can interfere).
- **Scan-time control throttle:** while `scanningNodeId` matches a node's id, that node's volume sliders and mute toggles are also disabled (pointer-events: none + visual dim). This prevents a race where local client UI mutations fire while the Master's device cache for that node is partially stale from scan-induced disconnects and delayed WS delta syncs.
- **Post-scan state snap guard:** when `scanningNodeId` clears (scan complete or sheet closed), the UI must call `load()` (full `GET /devices` refresh) and wait for the response to settle before restoring pointer events on the affected section's controls. This prevents sliders from snapping to stale local state that drifted during the scan. `reconnecting` already guards the `[+ Add]` button; this extends the same principle to the control surface.

### `api.js` change

```js
export async function api(method, path, body, nodeId) {
  const prefix = nodeId ? `/nodes/${nodeId}` : ''
  // ...existing logic with `prefix + path`
}
```

Non-breaking — all existing callers pass no 4th argument.

### `App.svelte` changes

1. Fetch `GET /nodes` on mount alongside `GET /devices`
2. If `nodes.length > 1`: render `{#each nodes} <NodeSection>` grouped by `node_id`
3. Handle `satellite_online` / `satellite_offline` WS events → update `nodes` reactive array
4. Thread `nodeId` into `ScanSheet` from `NodeSection`
5. `ScanSheet` scopes its `GET /devices` call to the correct node (for `prevConnected` capture)

---

## Implementation Phases

### Phase 1 — RTP audio infrastructure ✓ DONE
`internal/audio/interface.go` + `internal/audio/rtp.go`. Unit-test parser with table of raw `pactl` outputs. Add stubs to MockController. **Why first:** isolating `pactl` quirks early avoids late surprises.

### Phase 2 — Mode/name flags (no behavior change)
Three new flags in `main.go`, three new `With*` options, new fields on `server` struct. Validate at startup. Standalone behavior unchanged.

### Phase 3 — Node registry + Master WS server
`noderegistry.go` + `satellite_server.go`. Registration, heartbeat watchdog, RTP lifecycle, `satellite_online`/`satellite_offline` events to UI hub.

### Phase 4 — Satellite WS client
`satellite_client.go`. Exponential backoff reconnect (1 s → 2 s → 4 s … 30 s cap) **with ±15% random jitter** applied to each delay. Jitter prevents all satellites from reconnecting in lockstep after a simultaneous power outage or network switch reboot (thundering herd). Register, pong loop, BT event forwarding via hub `Subscribe()`.

### Phase 5 — HTTP proxy
`proxy.go` + route registration. `/nodes/:id/*` → Satellite HTTP API. 404 for unknown id, 503 for offline. Stdlib `httputil.ReverseProxy` — no new vendor deps.

### Phase 6 — Unified `GET /devices` + `GET /nodes` REST
`GET /devices` reads from the Master's in-memory device cache (populated via WS push in Phase 3/4), stamps `node_id` per device, merges with local devices — O(1), no network calls. `GET /nodes` returns master entry + registry list. Remove the parallel fan-out approach entirely.

### Phase 7 — UI multi-node layout
`NodeSection.svelte`, `App.svelte` updates, `api.js` `nodeId` param, `ScanSheet` node scoping.

### Phase 8 — Lifecycle hardening
Offline detection UI state, reconnect backoff verification, Master crash recovery test.

### Phase 9 — Install script + docs
Satellite mode in `install.sh` (skip librespot). Update `echomux.service` systemd unit on Master to add:
```
Requires=pipewire-pulse-system.socket
After=pipewire-pulse-system.socket
```
This guarantees the PulseAudio compat IPC socket is fully bound before `cleanOrphanRTPModules()` runs `pactl` at startup. Without this, a fast-booting Pi may have `echomux` attempt `pactl` before the socket exists, causing a silent connection-refused failure. Promote this file to `ECHOMUX-SATELLITE.md`.

---

## Risks & Mitigations

| Risk | Mitigation |
|---|---|
| `pactl` output format varies by PipeWire version | Line-by-line scan for integer; table-driven unit tests with real output samples |
| RTP modules orphaned on Master crash | `cleanOrphanRTPModules()` on Master startup (mirrors `killOrphanLoopbacks`) |
| Duplicate Satellite name causes ID collision | Reject second connection with close message; log error — fail visibly |
| ~~Slow satellite stalls `GET /devices`~~ | Eliminated — `GET /devices` reads from in-memory cache, no per-request fan-out |
| Dirty reconnect before watchdog fires creates orphaned pactl module | Registration handler: cancel old context (fast, in lock) → release lock → async `RemoveRTPSink` → re-acquire lock to write new state |
| WS reconnect races | Mutex protects map reads/writes only; `pactl` IPC runs outside the lock to avoid stalling concurrent satellite heartbeats |
| `pactl` can't reach `pipewire-pulse` socket | `rtp.go` injects both `PIPEWIRE_RUNTIME_DIR` and `PULSE_SERVER` env vars; `echomux.service` adds `After=pipewire-pulse-system.socket` |
| Delta event cache drift from dropped WS frame or missed D-Bus signal | Sequence counter on delta events; Master sends `request_sync` on gap; Satellite full-push every 5 min |
| Multicast group collision across two echomux clusters on same LAN | `ECHOMUX_RTP_ADDRESS` / `ECHOMUX_RTP_PORT` flags; each cluster uses a distinct group |
| `POST /nodes/kitchen/devices/MAC/volume` returns 404 on satellite | `proxy.go` `Director` strips `/nodes/:id` prefix before forwarding |
| Master fd/port exhaustion from idle proxy connections to dead satellites | Custom `http.Transport`: `MaxIdleConnsPerHost: 2`, `IdleConnTimeout: 30s`, `ResponseHeaderTimeout: 5s` |
| Bloated / malicious `devices` payload OOMs Master | `conn.SetReadLimit(512 KiB)` immediately after WS handshake on both ends |
| `request_sync` storm after satellite reboot (seq resets to 0) | `register` message unconditionally resets Master's seq tracking for that node |
| Stale satellite device data served during async teardown window | `Online = false` + `Devices = nil` set inside lock before release; `GET /devices` skips offline entries |
| Satellite `pw-loopback` connects to user-session PipeWire instead of system graph | `defaultSpawn` env injection must be unconditional (not mode-gated); verified for satellite mode |
| `cleanOrphanRTPModules` kills unrelated system audio modules | Parse module args; only unload lines matching `port=<ECHOMUX_RTP_PORT>` and `destination_ip=<ECHOMUX_RTP_ADDRESS>` |
| Master self-ingests own multicast stream causing echo | `pactl load-module module-rtp-send … loop=0` in multicast mode |
| Proxy propagates satellite BlueZ hang as generic 500 | `ModifyResponse` maps 5xx/timeout → 504 + `{"error":"bluetooth_subsystem","node":"id"}` |
| Slow satellite WS connection holds unbounded heap on Master | Write deadline (`now + 5s`) before each outbound WS push on both ends |
| `pactl` hangs forever when PipeWire daemon is zombie | `exec.CommandContext` with 3 s deadline; kills child on expiry, logs warning, goroutine exits |
| All satellites reconnect simultaneously after power outage (thundering herd) | ±15% random jitter on each backoff delay in `satellite_client.go` |
| echomux system user blocked from user-space pulse socket (DAC) | System path (`/run/pipewire/pulse-native`) mandatory; user-path fallback requires group/ACL provisioning in install script |
| Slider snaps to stale local state when scan completes | `load()` awaited before pointer events restored on the completing section |
| Audio clock drift causing pops over long sessions | Satellite `10-rtp-source.conf` sets `latency.msec=200`; PipeWire adaptive resampler handles drift without stream reset |

---

## Architectural Refinements (incorporated from review)

**V1:**
1. **Device cache over fan-out** — `GET /devices` reads from Master's in-memory cache; Satellite pushes device list after registration and sends delta events.
2. **Dirty reconnect guard** — registration handler: cancel context (in lock) → release lock → async `RemoveRTPSink` → re-acquire to write new state.
3. **PulseAudio socket** — `rtp.go` injects `PULSE_SERVER` env var; install script confirms `pipewire-pulse-system.service` active.
4. **RTP multicast option** — unicast first, multicast as Phase 9 upgrade path for N > 2 satellites.
5. **Clock drift** — Satellite `10-rtp-source.conf` sets `latency.msec=200`; PipeWire adaptive resampler handles drift.
6. **ScanSheet global lockout** — `scanningNodeId` in `App.svelte`; disables `[+ Add]` on all sections while any scan is active.

**V2:**
7. **Mutex scope** — `pactl` IPC runs outside the registry lock; critical section covers map reads/writes only.
8. **Delta drift self-healing** — sequence counter on delta events + `request_sync` message + 5-minute full-push timer.
9. **Systemd socket ordering** — `echomux.service` (Master) adds `Requires/After=pipewire-pulse-system.socket`.
10. **RTP address/port flags** — `ECHOMUX_RTP_ADDRESS` + `ECHOMUX_RTP_PORT` for multi-cluster isolation.
11. **Proxy path stripping** — `proxy.go` `Director` strips `/nodes/:id` prefix before forwarding.

**V3:**
12. **Proxy Transport** — explicit `http.Transport`: `MaxIdleConnsPerHost: 2`, `IdleConnTimeout: 30s`, `ResponseHeaderTimeout: 5s`, dial timeout 5 s; prevents fd leak on dirty satellite drops.
13. **WS read limit** — `conn.SetReadLimit(512 KiB)` on both server and client after handshake; guards against bloated payloads.
14. **Sequence reset on `register`** — Master resets seq counter unconditionally to 0 on each registration; prevents false `request_sync` storm after satellite reboot.
15. **Cache invalidation before async teardown** — `nodeInfo.Online = false` + `nodeInfo.Devices = nil` inside the lock before releasing; `GET /devices` skips `Online == false` entries.
16. **Satellite loopback env parity** — `defaultSpawn` env injection (`PIPEWIRE_RUNTIME_DIR`) must be unconditional, not mode-gated; verified for satellite mode.
17. **`pactl` module ID regex** — `regexp.MustCompile(`^\s*([0-9]+)\s*$`)` over stdout lines, warning lines skipped; no index cutting.

**V4:**
18. **Targeted orphan purge** — `cleanOrphanRTPModules` inspects module argument strings; only unloads entries matching `ECHOMUX_RTP_PORT`/`ECHOMUX_RTP_ADDRESS`; avoids killing unrelated system audio modules.
19. **Multicast `loop=0`** — `pactl load-module module-rtp-send` in multicast mode appends `loop=0` to prevent host self-ingestion of its own multicast stream.
20. **Proxy 504 isolation** — `ModifyResponse` in `proxy.go` maps satellite 5xx/timeout to HTTP 504 + `{"error":"bluetooth_subsystem","node":"<id>"}` so UI shows a node-scoped BT error, not a full-node offline state.
21. **WS write deadlines** — outbound pushes on both server and client set `conn.SetWriteDeadline(now + 5s)` before each write; prevents slow/stalled connections from holding unbounded heap.
22. **Scan-time control throttle** — while `scanningNodeId` matches a node, that node's volume sliders and mute toggles are disabled; prevents client mutation races against stale cached state.

**V5:**
23. **`exec.CommandContext` with 3 s deadline on all `pactl` calls** — zombie PipeWire/pulse daemon cannot hang async teardown goroutine indefinitely; child process is killed on deadline, warning logged, goroutine exits cleanly.
24. **Reconnect jitter ±15%** — satellite backoff delays randomized to prevent thundering herd after simultaneous power recovery.
25. **System socket path enforced** — `PULSE_SERVER` defaults to `unix:/run/pipewire/pulse-native`; install script gates on system path; user-path fallback requires explicit group/ACL provisioning.
26. **Post-scan state snap guard** — `load()` called and awaited before pointer events restored on scan-completing section; prevents slider snap to drifted local state.

---

## Design Gaps (resolve before coding)

1. **Satellite `addr` in register message** — Satellite must send `"addr":"IP:port"` explicitly since `r.RemoteAddr` gives the ephemeral source port. Port comes from the Satellite's `--addr` flag.

2. **`GET /devices` on Satellite standalone** — Satellite's own HTTP API returns devices without `node_id` (local view). Master stamps `node_id` when aggregating. Direct browser access to Satellite's IP works as a diagnostic view.

3. **`ScanSheet` node scoping** — `ScanSheet` calls `GET /devices` internally to capture `prevConnected`. It must call the scoped version so it only reconnects speakers belonging to its own node. Pass `nodeId` prop from `NodeSection` into `ScanSheet`.

4. **Global vs per-node Restart button** — The global `[↻]` in the app header remains as a Master-only control. Per-node Restart lives in `NodeSection`. Document the distinction clearly in UI.

5. **Satellite `autorouter` behavior** — `ensureMainMixLink` in Satellite mode links `rtp-source` (UDP :9001, receiving Master RTP) → `main-mix`. This is identical to standalone behavior. **No `autorouter.go` changes needed for Satellite mode.**

6. **librespot on Satellite** — Install script must skip librespot on Satellite. If librespot runs on Satellite, its PCM mixes into `main-mix` alongside Master's RTP stream, corrupting audio.

7. **`pactl` socket path** — The canonical socket for system-wide pipewire-pulse is `/run/pipewire/pulse-native`. This **must** be used when echomux runs as a system service; user-space paths like `/run/user/1000/pulse/native` require DAC group membership that a dedicated `echomux` system user will not have by default. The install script must: (a) use the system path, and (b) if the deployment exceptionally requires a user-space path, explicitly add the `echomux` service account to the relevant audio group or set socket ACLs. Make the path configurable via `ECHOMUX_PULSE_SERVER` with default `unix:/run/pipewire/pulse-native`.

8. **`10-rtp-source.conf` satellite variant** — The current config listens on `0.0.0.0:9001` with no multicast group. For the unicast phase this is fine. For the multicast upgrade, a separate `10-rtp-source-satellite.conf` (or a mode-conditional install step) must join the multicast group. Decide config management strategy before Phase 9.

---

## Testing Strategy

### Unit (Go)
- `internal/audio/rtp_test.go` — pactl output parser edge cases (trailing newline, warning prefix, empty output)
- `internal/api/satellite_server_test.go` — register, heartbeat timeout, RTP module lifecycle, event broadcast to UI clients
- `internal/api/satellite_client_test.go` — reconnect backoff, pong response, event forwarding
- `internal/api/proxy_test.go` — happy path, 404 unknown, 503 offline, path stripping
- `internal/api/handlers_test.go` — extend `TestGetDevices` for master-mode cache read with `node_id` (no HTTP fan-out; populate cache directly via `nodeRegistry`)

### Integration (Go)
Start both Master and Satellite as `httptest.Server` instances with mock BT/audio. Verify round-trip: Satellite BT event → Master `/events` WS with `node_id`. Verify proxy: `GET /nodes/kitchen/devices` returns Satellite devices.

### UI (Vitest)
- `NodeSection.test.js` — offline state, button scoping, `nodeId` threading
- `api.test.js` — `nodeId` parameter
- `App.test.js` — `satellite_online`/`satellite_offline` WS event handling

### Hardware E2E
1. Flash two Pis with master/satellite config
2. Verify UI shows two sections
3. Add BT speaker on Satellite from Master UI
4. Verify audio plays on Satellite speakers
5. Kill Satellite → verify Master section continues, Satellite section grays out
6. Restart Satellite → verify reconnect ≤30 s, audio resumes