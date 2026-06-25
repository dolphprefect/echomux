# echomux HTTP & WebSocket API

Base URL: `http://<host>:56644`

No authentication. All request and response bodies are JSON unless noted. Error responses return plain text with an appropriate HTTP status code.

---

## Modes

echomux operates in one of three modes configured by `ECHOMUX_MODE`:

| Mode | Description |
|---|---|
| `standalone` | Single-node; all endpoints operate locally (default) |
| `master` | Multi-node master; accepts satellite WebSocket connections on `/nodes`; proxies REST calls to satellites via `/nodes/{id}/...` |
| `satellite` | Connects to master via `/nodes` WebSocket; exposes its own REST API on its local port; audio comes from master via RTP unicast |

In `standalone` mode there is only one node and `/nodes` returns a single-element list.

---

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/nodes` | List all nodes (master + satellites) |
| `ANY` | `/nodes/{id}/...` | Proxy any endpoint to a satellite node |
| `GET` | `/devices` | List known speakers with current state (all nodes aggregated) |
| `POST` | `/scan` | Scan for nearby Bluetooth devices |
| `POST` | `/devices/:mac/connect` | Connect a speaker |
| `POST` | `/devices/:mac/disconnect` | Disconnect a speaker |
| `POST` | `/devices/:mac/pair` | Pair a device found during scan |
| `DELETE` | `/devices/:mac` | Forget a speaker (unpair + remove) |
| `PUT` | `/devices/:mac/volume` | Set volume (0–100) |
| `PUT` | `/devices/:mac/mute` | Set mute state |
| `PUT` | `/devices/:mac/delay` | Set per-speaker delay (0–2000 ms) |
| `POST` | `/playback/pause` | Pause all audio loopbacks |
| `POST` | `/playback/resume` | Resume audio loopbacks |
| `POST` | `/playback/restart` | Kill and respawn all loopbacks |
| `GET` | `/input` | List available PipeWire source nodes |
| `POST` | `/input/discover` | Make the Pi discoverable as a BT input target |
| `GET` | `/stream` | Check whether a Spotify stream is active |
| `GET` | `/events` | WebSocket — real-time BT and loopback events |
| `WS` | `/nodes` | WebSocket — satellite registration (master only; used by satellite process, not UI) |

---

## GET /nodes

Returns the list of all known nodes: the local master plus any connected satellites.

**Response 200:**

```json
[
  { "id": "living-room", "name": "living-room", "role": "master",    "online": true,  "addr": "" },
  { "id": "bedroom",     "name": "bedroom",     "role": "satellite", "online": true,  "addr": "192.168.1.10:56644" },
  { "id": "kitchen",     "name": "kitchen",     "role": "satellite", "online": false, "addr": "192.168.1.11:56644" }
]
```

| Field | Type | Description |
|---|---|---|
| `id` | string | URL-safe slug derived from `name` (lowercase, spaces → `-`). Used in `/nodes/{id}/...` proxy paths |
| `name` | string | Human-readable node name set via `ECHOMUX_NAME` |
| `role` | string | `"master"` or `"satellite"` |
| `online` | bool | Whether the satellite's WebSocket connection to master is currently active (`true` for master always) |
| `addr` | string | Satellite's public `host:port`; empty for master |

In standalone mode this always returns a single entry with `role: "master"` and `online: true`.

---

## ANY /nodes/{id}/...

Proxies any endpoint to the satellite identified by `{id}`. The `{id}` is the node's `id` field from `GET /nodes`.

Examples:

```
GET  /nodes/bedroom/devices          → GET /devices on the bedroom satellite
POST /nodes/bedroom/scan             → POST /scan on the bedroom satellite
POST /nodes/bedroom/playback/restart → POST /playback/restart on the bedroom satellite
```

The master forwards the full request (method, body, query string) and streams the response back. If the satellite is offline the proxy returns `502 Bad Gateway`.

The master's own endpoints are accessed directly (no `/nodes/{id}/` prefix needed, or use the master's own node ID).

---

## GET /devices

Returns the list of known speakers across **all nodes** (master + online satellites).

Each device includes a `node_id` field identifying which node manages it. Devices on the master have `node_id` omitted.

**Response 200:**

```json
[
  {
    "MAC": "C8:24:78:67:83:C0",
    "Name": "EDIFIER MP330",
    "Connected": true,
    "Paired": true,
    "playing": true,
    "delay_ms": 250,
    "volume": 74,
    "muted": false
  },
  {
    "MAC": "AA:BB:CC:DD:EE:FF",
    "Name": "Bedroom Speaker",
    "Connected": true,
    "Paired": true,
    "playing": true,
    "delay_ms": 0,
    "volume": 60,
    "muted": false,
    "node_id": "bedroom"
  }
]
```

| Field | Type | Description |
|---|---|---|
| `MAC` | string | Bluetooth MAC address — used as the identifier in all other endpoints |
| `Name` | string | Bluetooth device name |
| `Connected` | bool | BT connection is currently up |
| `Paired` | bool | Device is paired with the host adapter |
| `playing` | bool | Audio loopback is alive — audio is actively flowing |
| `delay_ms` | int | Per-speaker delay in milliseconds (0–2000) |
| `volume` | int | Volume 0–100; `-1` means the PipeWire node has not registered yet |
| `muted` | bool | Whether the speaker is muted |
| `node_id` | string | Which satellite manages this device; omitted for master devices |

Returns an empty array `[]` when no speakers have been added yet.

---

## POST /scan

Starts a Bluetooth discovery scan. While scanning, all active speakers on the target node are temporarily disconnected and audio is paused — classic BT inquiry and A2DP streams share the same radio.

To scan on a satellite node, use the proxy: `POST /nodes/{id}/scan`.

**Request body** (optional):

```json
{ "timeout_sec": 10 }
```

`timeout_sec` defaults to 10. Must be > 0.

**Response 200** — always returns an envelope:

```json
{ "devices": [
    { "MAC": "C8:24:78:67:83:C0", "Name": "EDIFIER MP330", "Connected": false, "Paired": false }
  ]
}
```

On scan failure the `devices` array is empty and an `error` field describes the failure:

```json
{ "devices": [], "error": "scan timed out" }
```

The HTTP status is always `200 OK`. An empty `devices` array with no `error` field is a valid empty scan result (no devices found). Check for the presence of `error` to distinguish a failed scan from a successful scan that found nothing.

**Note:** Response headers are flushed immediately when the scan starts so that reverse proxies (including the master's node proxy) do not time out waiting for headers during the scan.

**After the scan sheet closes**, call `POST /playback/resume` to reconnect speakers and restore audio. The server does not auto-resume — the client owns this lifecycle.

---

## POST /devices/:mac/connect

Connects to a Bluetooth speaker. Retries up to 3 times with a 2 s gap on transient radio errors.

Returns as soon as the BT connection succeeds. The audio loopback starts asynchronously within a few seconds — watch for `loopback_started` on the WebSocket.

"AlreadyConnected" errors from BlueZ are treated as success.

**Returns:** 204 No Content | 404 Not Found | 503 (request cancelled) | 500

---

## POST /devices/:mac/disconnect

Disconnects the speaker and kills its audio loopback.

**Returns:** 204 No Content | 404 Not Found | 500

---

## POST /devices/:mac/pair

Pairs the host adapter with a device discovered during a scan. The device must be in pairing mode. If BlueZ has already forgotten the device since the scan completed, echomux runs a short 6 s re-scan and retries automatically.

"Already Exists" / "AlreadyExists" errors from BlueZ are treated as success (device is already paired).

Follow with `POST /devices/:mac/connect` to connect after pairing.

**Returns:** 204 No Content | 404 Not Found (device not found even after re-scan) | 500

---

## DELETE /devices/:mac

Forgets a speaker: stops its loopback, disconnects, unpairs from BlueZ, and removes it from the known speaker list. The device will no longer appear in `GET /devices`.

If BlueZ has already forgotten the device (e.g. factory reset), the local state is still cleaned up and 204 is returned.

**Returns:** 204 No Content | 500

---

## PUT /devices/:mac/volume

Sets the speaker's volume. Applied immediately to the PipeWire node via `wpctl`.

**Request body:**

```json
{ "level": 74 }
```

`level` must be 0–100 (inclusive).

**Returns:** 204 No Content | 400 Bad Request | 404 Not Found (PipeWire node not present) | 500

---

## PUT /devices/:mac/mute

Mutes or unmutes the speaker.

**Request body:**

```json
{ "muted": true }
```

**Returns:** 204 No Content | 400 Bad Request | 404 Not Found | 500

---

## PUT /devices/:mac/delay

Sets the per-speaker delay. This kills and respawns the audio loopback — there will be a brief audio gap on this speaker only. Other speakers are not affected.

**Request body:**

```json
{ "ms": 250 }
```

`ms` must be 0–2000 (inclusive).

**Returns:** 204 No Content | 400 Bad Request | 500

---

## POST /playback/pause

Pauses all audio loopbacks and prevents the autorouter from starting new ones. The BT connections stay up.

Use this before scanning (already done internally by `POST /scan`) or before any operation that requires a quiet radio.

**Returns:** 204 No Content

---

## POST /playback/resume

Unpauses the autorouter. Loopbacks for currently connected speakers restart within ~2 seconds.

**Returns:** 204 No Content

---

## POST /playback/restart

Kills all loopback processes without disconnecting Bluetooth. The autorouter respawns them within ~2 seconds. Also clears the zombie watchdog cooldowns so the watchdog acts immediately after restart.

Use this when speakers are connected but silent (zombie loopbacks).

**Returns:** 204 No Content

---

## GET /input

Returns all PipeWire `Audio/Source` nodes currently present in the graph.

**Response 200:**

```json
[
  { "ID": 99, "MAC": "", "Name": "rtp-source" }
]
```

`rtp-source` is always present when librespot is running. An additional entry appears when a phone connects to the Pi as a Bluetooth A2DP source.

**Returns:** 200 OK | 500

---

## POST /input/discover

Makes the Pi's Bluetooth adapter discoverable and pairable for a limited time. Use this to connect a phone as an audio input source (phone-as-speaker mode).

**Request body** (optional):

```json
{ "enabled": true, "timeout_sec": 60 }
```

Both fields default to `true` and `60` respectively.

**Returns:** 204 No Content | 500

---

## GET /stream

Reports whether an active Spotify Connect stream is present in the PipeWire graph.

**Response 200 — stream active:**

```json
{ "active": true, "source_node_id": 99 }
```

**Response 200 — no stream:**

```json
{ "active": false }
```

`source_node_id` is the PipeWire node ID of the active `rtp-source` node. It is omitted when `active` is `false`.

**Returns:** 200 OK | 500

---

## GET /events — WebSocket

```
ws://<host>:56644/events
```

Persistent WebSocket connection for real-time state updates. Each message is a JSON object:

```json
{ "type": "loopback_started", "mac": "C8:24:78:67:83:C0" }
```

| `type` | Fields | Meaning |
|---|---|---|
| `connected` | `mac` | Bluetooth connection established |
| `disconnected` | `mac` | Bluetooth connection dropped |
| `paired` | `mac` | Bluetooth pairing completed |
| `loopback_started` | `mac` | Audio loopback is live — speaker is now playing |
| `loopback_stopped` | `mac` | Audio loopback died — speaker is connected but silent |
| `satellite_online` | `id`, `name` | A satellite node connected to the master |
| `satellite_offline` | `id`, `name` | A satellite node disconnected from the master |

`satellite_online` / `satellite_offline` are only emitted by master nodes. On receiving either, refresh `GET /nodes` and `GET /devices`.

The server accepts any Origin (no CSRF protection — appropriate for a local network device).

Clients should reconnect automatically on close. Poll `GET /devices` every 5 s as a fallback.

---

## WS /nodes — Satellite control plane

```
ws://<master-host>:56644/nodes
```

Used by the satellite process to register with the master. Not intended for UI clients.

**Protocol:**

1. Satellite connects and immediately sends a `register` message:

```json
{ "type": "register", "name": "bedroom", "addr": "192.168.1.10:56644" }
```

`addr` is the satellite's public `host:port` for HTTP proxy. If omitted or port-only (`:56644`), the master derives the IP from the connection's remote address.

2. Master replies with a `registered` message:

```json
{ "type": "registered", "id": "bedroom" }
```

`id` is the URL-safe slug derived from the satellite's name (used in `/nodes/{id}/...` proxy paths).

3. The connection stays open as a heartbeat. The master broadcasts `satellite_online` to `/events` clients. The satellite's device list is cached on the master (pushed after registration and updated via delta events) for `GET /devices` aggregation. The satellite sends BT events upstream as `event` messages so they can be re-broadcast to `/events` clients on the master.

4. When the connection closes, the master marks the node offline and broadcasts `satellite_offline` to all `/events` clients.
