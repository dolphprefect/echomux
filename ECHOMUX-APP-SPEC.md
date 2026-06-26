# echomux Web UI — Spec

This document describes the web UI that ships embedded in the echomux binary. It is a Svelte single-page application compiled into the static assets served at `/`. The full API contract is in **[ECHOMUX-API.md](ECHOMUX-API.md)**.

---

## Components

```
App (root)
├── NodeSection          — one per node in multi-node mode
│   └── DeviceCard       — one per speaker
├── DeviceCard           — used directly in single-node mode
├── ScanSheet            — slide-up overlay for adding a speaker
└── DelaySheet           — slide-up overlay for adjusting per-speaker delay
```

---

## API routing — the nodeApiId invariant

All API calls go through `api(method, path, body, nodeId)` in `lib/api.js`:

```js
const url = nodeId ? `/nodes/${nodeId}${path}` : path
```

When `nodeId` is truthy the request is proxied through the master to a satellite. When `nodeId` is `undefined` or `null` the request hits the master directly.

`GET /devices` returns a `node_id` field on every device, including master devices. Master devices carry the master's own node ID (e.g. `"living-room"`). Passing that ID to `api()` would route the request to `/nodes/living-room/...`, which the proxy cannot handle because it only knows satellite nodes — this returns 404.

`App.svelte` resolves this with `nodeApiId()`:

```js
$: masterNodeId = masterNode ? masterNode.id : ''

function nodeApiId(nodeId) {
  return nodeId === masterNodeId ? undefined : nodeId
}
```

Every call that originates in `App.svelte` (connect, disconnect, forget, delay, volume restart, scan, playback resume) passes its `nodeId` argument through `nodeApiId()` before handing it to `api()`. Components that accept a `nodeId` prop (ScanSheet, DelaySheet) receive the already-converted value — they must not use `device.node_id` directly.

---

## App initialisation and state

On mount, `App` fetches `GET /devices` and `GET /nodes` in parallel, then opens a WebSocket to `/events`. Every full reload normalises the API's lowercase `muted`/`playing` fields to the capitalised `Muted`/`Playing` that components read.

Devices that are mid-connect (in the `connecting` Set) are kept visually connected across a reload by forcing `Connected: true` during normalisation.

WebSocket reconnects with a 3-second fixed delay. On reconnect the full load fires again via `ws.onopen`.

---

## Layout modes

**Single-node** (`nodes.length <= 1`): all devices rendered as a flat sorted list of `DeviceCard`s directly in `App`. The header shows a global restart button and a global add-speaker (+) button.

**Multi-node** (`nodes.length > 1`): devices grouped by node and rendered through `NodeSection`. The global + button is hidden; each `NodeSection` has its own scan and restart buttons. The master node section receives devices whose `node_id` matches the master id, plus any devices with no `node_id`.

Device sort order: connected speakers first, then alphabetical by name.

---

## WebSocket event handling

| Event type | Effect |
|---|---|
| `connected` | Set `device.Connected = true` |
| `disconnected` | Set `device.Connected = false`, `device.Playing = false` |
| `loopback_started` | Set `device.Playing = true` |
| `loopback_stopped` | Set `device.Playing = false` |
| `satellite_online` | Full reload (`GET /devices` + `GET /nodes`) |
| `satellite_offline` | Full reload |

All other event types are silently ignored. Events for unknown MACs are ignored.

---

## DeviceCard

Displays one speaker. State derives from the `device` prop:

- Dot indicator: `dot` (disconnected), `dot on` (connected), `dot on playing` (loopback running)
- Card class: default (connected), `connecting` (in-flight), `offline` (disconnected)

**Connected state controls:**
- Delay chip — shows `device.delay_ms` (or 0); tapping dispatches `openDelay` with the device object. The chip is disabled during a scan (`disabled` prop).
- Disconnect button (power icon) — dispatches `disconnect` with `{ mac, nodeId: device.node_id }`.
- Volume slider (0–100) — local state tracks the drag position. On `input`, updates `localVol` without calling the API. On `change` (release), commits the value via `PUT /devices/{mac}/volume` and dispatches `volumeChange`. Volume calls use `device.node_id` directly (not converted through `nodeApiId`) — these are fire-and-forget with no revert on failure.
- Mute button — optimistic toggle: dispatches `muteChange`, calls `PUT /devices/{mac}/mute`, reverts and dispatches again on failure. Also uses `device.node_id` directly.

**Disconnected state controls:**
- Forget button (trash icon) — dispatches `forget` with `{ mac, nodeId: device.node_id }`.
- Connect button — dispatches `connect` with `{ mac, nodeId: device.node_id }`.

Volume slider is disabled when volume is `< 0` (PipeWire node not yet created) or when the `disabled` prop is set.

---

## NodeSection

Wraps a node header and a grid of `DeviceCard`s. When the node is offline, the section is dimmed and cards are replaced with "Node is offline." When a scan is active for this node (`scanningNodeId === node.id`), the card grid receives `pointer-events: none` and 65% opacity, and the add button shows a spinner.

Satellite sections do not show controls for offline nodes — the restart and add buttons are hidden when `isOffline`.

---

## ScanSheet

Opens when the user taps a node's add button. On mount:
1. Calls `GET /devices` (via `nodeId`) to record which speakers were connected before the scan.
2. Calls `POST /scan` with `{ timeout_sec: 10 }` (via `nodeId`).
3. Filters results against `knownMACs` to hide already-paired devices.

The `nodeId` prop is the already-converted value from `nodeApiId()` — for master scans it is `undefined`, for satellite scans it is the satellite's ID.

Tapping "Add" on a result calls `POST /devices/{mac}/pair` then `POST /devices/{mac}/connect` (both via `nodeId`). State tracks each device independently: `loading` → `done` or `error`. The sheet auto-closes 800ms after the last in-flight add completes, provided no adds errored.

"Scan again" re-runs the scan without closing the sheet.

On close the sheet dispatches `{ prevConnected }`. `App` then calls `POST /playback/resume` (via `nodeApiId`) and reconnects each previously-connected speaker.

---

## DelaySheet

Opens over a specific device. Accepts two props: `device` and `nodeId`.

`nodeId` must be the value from `nodeApiId(device.node_id)`, not `device.node_id` directly. For master devices, `device.node_id` equals the master's node ID — passing it raw to `api()` routes the request through the proxy which returns 404. `App.svelte` always passes `nodeId={nodeApiId(delayDevice.node_id)}`.

The slider (0–2000 ms) updates the local `ms` display on `input` without calling the API. On `change` (slider release) or nudge button click, the value is clamped and committed via `PUT /devices/{mac}/delay`. On API failure the value reverts to the pre-commit value. On success the `updated` event is dispatched so `App` can sync `device.delay_ms`.

Nudge buttons: −50, −10, +10, +50 ms. All go through the same `commit()` path.

---

## Restart audio

**Global restart** (header button): calls `POST /playback/restart` with no node prefix, waits 2500ms, then reloads. Disabled while any restart or reconnect is in progress.

**Per-node restart** (`NodeSection` restart button): calls `POST /playback/restart` via `nodeApiId(nodeId)`, waits 2500ms, then reloads. A separate `restartingNodeId` tracks which node is restarting. The global restart button is disabled while any per-node restart is running.

---

## Error states

- **Load error**: replaces the device list with "Can't reach echomux" and a "Try again" button that re-runs `load()`.
- **Connect error**: shown inline on the card for 5 seconds. Errors containing `org.bluez` are displayed as "Connection failed".
- **Scan error**: shown inline in the ScanSheet; "Scan again" is always available.
- **Delay commit failure**: silently reverts the slider and readout to the previous value.
- **Volume failure**: silently ignored (fire-and-forget).
- **Mute failure**: reverts via a second `muteChange` dispatch.
- **Disconnect failure**: reverts `Connected` back to true in local state.
