# echomux HTTP & WebSocket API

Base URL: `http://<host>:56644`

No authentication. All request and response bodies are JSON unless noted. Error responses return plain text with an appropriate HTTP status code.

---

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/devices` | List known speakers with current state |
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

---

## GET /devices

Returns the list of known speakers.

Only devices that have previously appeared as a `bluez_output.*` PipeWire node are returned. A newly paired device will appear after its first successful connection.

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

Returns an empty array `[]` when no speakers have been added yet.

---

## POST /scan

Starts a Bluetooth discovery scan. While scanning, all active speakers are temporarily disconnected and audio is paused — classic BT inquiry and A2DP streams share the same radio.

**Request body** (optional):

```json
{ "timeout_sec": 10 }
```

`timeout_sec` defaults to 10. Must be > 0.

**Response 200** — array of all discovered Bluetooth devices (not filtered to known speakers):

```json
[
  { "MAC": "C8:24:78:67:83:C0", "Name": "EDIFIER MP330", "Connected": false, "Paired": false }
]
```

**After the scan sheet closes**, call `POST /playback/resume` to reconnect speakers and restore audio. The server does not auto-resume — the client owns this lifecycle.

**Returns:** 200 OK | 500

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

| `type` | `mac` | Meaning |
|---|---|---|
| `connected` | speaker MAC | Bluetooth connection established |
| `disconnected` | speaker MAC | Bluetooth connection dropped |
| `paired` | speaker MAC | Bluetooth pairing completed |
| `loopback_started` | speaker MAC | Audio loopback is live — speaker is now playing |
| `loopback_stopped` | speaker MAC | Audio loopback died — speaker is connected but silent |

The server accepts any Origin (no CSRF protection — appropriate for a local network device).

Clients should reconnect automatically on close. Poll `GET /devices` every 5 s as a fallback.
