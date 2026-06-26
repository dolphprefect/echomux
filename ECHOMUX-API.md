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
| `satellite` | Connects to master via `/nodes` WebSocket; exposes its own REST API on its local port; receives audio from master via RTP unicast |

In `standalone` mode there is only one node and `/nodes` returns a single-element list.

---

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/nodes` | List all nodes (master + satellites) |
| `ANY` | `/nodes/{id}/...` | Proxy any endpoint to a satellite node |
| `GET` | `/devices` | List known speakers with current state (all nodes aggregated) |
| `POST` | `/scan` | Scan for nearby Bluetooth devices |
| `POST` | `/devices/{mac}/connect` | Connect a speaker |
| `POST` | `/devices/{mac}/disconnect` | Disconnect a speaker |
| `POST` | `/devices/{mac}/pair` | Pair a device found during scan |
| `DELETE` | `/devices/{mac}` | Forget a speaker (unpair + remove) |
| `PUT` | `/devices/{mac}/volume` | Set volume (0–100) |
| `PUT` | `/devices/{mac}/mute` | Set mute state |
| `PUT` | `/devices/{mac}/delay` | Set per-speaker delay (0–2000 ms) |
| `POST` | `/playback/pause` | Pause all audio loopbacks |
| `POST` | `/playback/resume` | Resume audio loopbacks |
| `POST` | `/playback/restart` | Kill and respawn all loopbacks |
| `GET` | `/input` | List available PipeWire source nodes |
| `POST` | `/input/discover` | Make the Pi discoverable as a BT input target |
| `GET` | `/stream` | Check whether an RTP audio stream is active |
| `GET` | `/events` | WebSocket — real-time BT and loopback events |
| `WS` | `/nodes` | WebSocket — satellite registration (master only; used by satellite process, not UI) |

---

## GET /nodes

Returns the list of all known nodes: the local master plus any connected or previously connected satellites.

A GET request and a WebSocket upgrade request both arrive on the same `/nodes` path. The server routes based on the `Upgrade` header: an HTTP GET without `Upgrade: websocket` returns the node list; a WebSocket upgrade initiates a satellite session.

**Response 200:**

```json
[
  { "id": "living-room", "name": "living room", "role": "master",    "online": true,  "addr": "" },
  { "id": "fitness-room", "name": "fitness room", "role": "satellite", "online": true,  "addr": "192.168.1.2:56644" },
  { "id": "bedroom",      "name": "bedroom",      "role": "satellite", "online": false, "addr": "192.168.1.10:56644" }
]
```

| Field | Type | Description |
|---|---|---|
| `id` | string | URL-safe slug derived from `name` (`strings.ToLower`, spaces → `-`). Used in `/nodes/{id}/...` proxy paths |
| `name` | string | Human-readable node name set via `ECHOMUX_NAME` |
| `role` | string | `"master"` or `"satellite"` |
| `online` | bool | Whether the satellite's WebSocket connection to master is currently active. Always `true` for the master itself |
| `addr` | string | Satellite's public `host:port`; empty for master |

In standalone mode this always returns a single entry with `role: "master"` and `online: true`.

---

## ANY /nodes/{id}/...

Proxies any request to the satellite identified by `{id}`. The `{id}` is the node's `id` field from `GET /nodes`. The master strips the `/nodes/{id}` prefix and forwards the full request (method, body, query string) to the satellite's HTTP API.

**Error responses from the proxy:**

| Status | Meaning |
|---|---|
| `404 Not Found` | Node ID not registered |
| `503 Service Unavailable` | Node is registered but currently offline |
| `504 Gateway Timeout` | Dial to satellite failed, or satellite returned a 5xx response |

The master's own endpoints are accessed directly without a `/nodes/{id}/` prefix.

---

## GET /devices

Returns known speakers across all nodes (master + online satellites). In master mode each device carries a `node_id` identifying which node manages it. In standalone mode `node_id` is omitted.

**Response 200:**

```json
[
  {
    "MAC": "C8:24:78:80:8B:66",
    "Name": "EDIFIER M60",
    "Connected": true,
    "Paired": true,
    "playing": true,
    "delay_ms": 0,
    "volume": 74,
    "muted": false,
    "node_id": "living-room"
  },
  {
    "MAC": "C8:24:78:67:83:C0",
    "Name": "EDIFIER MP330",
    "Connected": true,
    "Paired": true,
    "playing": true,
    "delay_ms": 250,
    "volume": 60,
    "muted": false,
    "node_id": "fitness-room"
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
| `node_id` | string | Which node manages this device (master mode only). Present for both master-local and satellite devices |

Returns an empty array `[]` when no speakers have been added yet.

---

## POST /scan

Starts a Bluetooth discovery scan. While scanning, all active speakers on the target node are temporarily disconnected and audio is paused — classic BT inquiry and A2DP streams share the same radio.

To scan on a satellite node, use the proxy: `POST /nodes/{id}/scan`.

**Request body** (optional):

```json
{ "timeout_sec": 10 }
```

`timeout_sec` defaults to 10 if absent or ≤ 0.

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

The HTTP status is always `200 OK`. An empty `devices` array with no `error` field means a successful scan found nothing. Check for the presence of `error` to distinguish a failed scan from an empty scan.

Response headers are flushed immediately when the scan starts so that the master's node proxy does not time out waiting for headers during the scan duration.

After the scan sheet closes, call `POST /playback/resume` to reconnect speakers and restore audio. The server does not auto-resume — the client owns the pause/resume lifecycle. If the client disconnects mid-scan the server auto-unpauses as a safety fallback.

---

## POST /devices/{mac}/connect

Connects to a Bluetooth speaker. Retries up to 3 times with a 2 s gap on transient radio errors. Returns as soon as the BT connection succeeds. The audio loopback starts asynchronously within a few seconds — watch for `loopback_started` on the WebSocket.

"AlreadyConnected" errors from BlueZ are treated as success.

**Returns:** 204 No Content | 404 Not Found | 503 (request cancelled) | 500

---

## POST /devices/{mac}/disconnect

Disconnects the speaker and kills its audio loopback.

**Returns:** 204 No Content | 404 Not Found | 500

---

## POST /devices/{mac}/pair

Pairs the host adapter with a device discovered during a scan. The device must be in pairing mode. If BlueZ has already removed the device from its cache since the scan completed, echomux runs a short 6 s re-scan and retries automatically.

"Already Exists" / "AlreadyExists" errors from BlueZ are treated as success.

Follow with `POST /devices/{mac}/connect` to connect after pairing.

**Returns:** 204 No Content | 404 Not Found (device not found even after re-scan) | 500

---

## DELETE /devices/{mac}

Forgets a speaker: stops its loopback, disconnects, unpairs from BlueZ, and removes it from the known speaker list. The device will no longer appear in `GET /devices`.

If BlueZ has already forgotten the device (e.g. factory reset), local state is still cleaned up and 204 is returned.

**Returns:** 204 No Content | 500

---

## PUT /devices/{mac}/volume

Sets the speaker's volume. Applied immediately to the PipeWire node via `wpctl`.

**Request body:**

```json
{ "level": 74 }
```

`level` must be 0–100 (inclusive).

**Returns:** 204 No Content | 400 Bad Request | 404 Not Found (PipeWire node not present) | 500

---

## PUT /devices/{mac}/mute

Mutes or unmutes the speaker.

**Request body:**

```json
{ "muted": true }
```

**Returns:** 204 No Content | 400 Bad Request | 404 Not Found | 500

---

## PUT /devices/{mac}/delay

Sets the per-speaker delay. If a loopback is running, it is killed and immediately respawned with the new delay — there will be a brief audio gap on this speaker only. Other speakers are not affected. The delay is persisted across restarts.

**Request body:**

```json
{ "ms": 250 }
```

`ms` must be 0–2000 (inclusive).

**Returns:** 204 No Content | 400 Bad Request | 500

---

## POST /playback/pause

Pauses all audio loopbacks and prevents the autorouter from starting new ones. BT connections stay up.

**Returns:** 204 No Content

---

## POST /playback/resume

Unpauses the autorouter. Loopbacks for currently connected speakers restart within ~2 seconds.

**Returns:** 204 No Content

---

## POST /playback/restart

Kills all loopback processes without disconnecting Bluetooth. Also kills any orphan `pw-loopback` processes not tracked by the current session. Clears zombie watchdog cooldowns so the watchdog can act immediately. The autorouter respawns loopbacks within ~2 seconds.

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

**Returns:** 200 OK | 500

---

## POST /input/discover

Makes the Pi's Bluetooth adapter discoverable and pairable for a limited time. Use this to connect a phone as an audio input source.

**Request body** (optional):

```json
{ "enabled": true, "timeout_sec": 60 }
```

Both fields default to `true` and `60` respectively if absent or zero.

**Returns:** 204 No Content | 500

---

## GET /stream

Reports whether an active RTP audio stream is present in the PipeWire graph (detected by finding a node named `rtp-source`).

**Response 200 — stream active:**

```json
{ "active": true, "source_node_id": 99 }
```

**Response 200 — no stream:**

```json
{ "active": false }
```

`source_node_id` is the PipeWire node ID of the active source. It is omitted when `active` is `false`.

**Returns:** 200 OK | 500

---

## GET /events — WebSocket

```
ws://<host>:56644/events
```

Persistent WebSocket connection for real-time state updates. Each message is a JSON object with at minimum a `type` field.

### Event types

| `type` | Fields | Meaning |
|---|---|---|
| `connected` | `mac` | Bluetooth connection established |
| `disconnected` | `mac` | Bluetooth connection dropped |
| `paired` | `mac` | Bluetooth pairing completed |
| `loopback_started` | `mac` | Audio loopback is live — speaker is now playing |
| `loopback_stopped` | `mac` | Audio loopback died — speaker is connected but silent |
| `satellite_online` | `node_id`, `name` | A satellite node connected to the master |
| `satellite_offline` | `node_id`, `name` | A satellite node disconnected from the master |

`satellite_online` / `satellite_offline` are only emitted by master nodes. On receiving either, refresh `GET /nodes` and `GET /devices`.

BT events (`connected`, `disconnected`) originating from a satellite also carry a `node_id` field identifying the satellite.

The server accepts any Origin (no CSRF protection — appropriate for a local network device).

---

## WS /nodes — Satellite control plane

```
ws://<master-host>:56644/nodes
```

Used by the satellite process to register with and stay connected to the master. Not intended for UI clients.

All messages in both directions share the `nodeWsMsg` shape:

```json
{ "type": "...", "id": "...", "name": "...", "addr": "...", "devices": [...], "mac": "...", "event": "...", "seq": 0 }
```

Fields not relevant to a given message type are omitted.

### Session lifecycle

**1. Satellite → Master: `register`**

Sent immediately after connecting.

```json
{ "type": "register", "name": "fitness room", "addr": "192.168.1.2:56644" }
```

`addr` is the satellite's public `host:port` for HTTP proxy. If the IP is missing or unroutable (`0.0.0.0`, `::`) the master derives it from the TCP connection's remote address.

If a live session for the same name already exists the master closes the new connection with `1008 Policy Violation`. The satellite must retry.

**2. Master → Satellite: `registered`**

```json
{ "type": "registered", "id": "fitness-room" }
```

`id` is the URL-safe slug derived from the satellite's name. The master also provisions an RTP unicast sink (via `pactl`) from master to satellite before sending this acknowledgement.

**3. Satellite → Master: `devices` (full push)**

Sent immediately after receiving `registered`, and again whenever the master sends `request_sync`.

```json
{ "type": "devices", "devices": [ { "MAC": "...", "Name": "...", "Connected": true, "playing": false, "delay_ms": 0, "volume": 60, "muted": false } ] }
```

The master caches this list and includes it in `GET /devices` responses.

**4. Satellite → Master: `event` (delta)**

Sent for each `connected` or `disconnected` BT event. Includes a monotonically increasing `seq` counter.

```json
{ "type": "event", "mac": "C8:24:78:67:83:C0", "event": "connected", "seq": 7 }
```

The master applies the delta to its cached device list and re-broadcasts the event to `/events` WebSocket clients. If a seq gap is detected (missing events), the master sends `request_sync` and discards the gapped event rather than broadcasting stale state.

A full `devices` push (from step 3 or a `request_sync` response) resets the seq acceptance state so the next delta is accepted regardless of its seq value.

In addition to BT events, the satellite also sends a full `devices` push when volume, mute, or delay state changes locally.

**5. Master → Satellite: `ping` / Satellite → Master: `pong`**

The master sends a `ping` every 10 seconds. The satellite must respond with `pong` within 5 seconds or the master closes the connection.

```json
{ "type": "ping" }
{ "type": "pong" }
```

**6. Master → Satellite: `request_sync`**

Sent when a seq gap is detected in delta events, or on a 5-minute periodic timer as a backstop.

```json
{ "type": "request_sync" }
```

The satellite responds by sending a fresh `devices` full push.

**7. Session end**

When the WebSocket connection closes (for any reason), the master marks the node offline, removes the RTP sink, and broadcasts `satellite_offline` to all `/events` clients. The satellite reconnects with exponential backoff (1 s base, 30 s cap, ±15% jitter).
