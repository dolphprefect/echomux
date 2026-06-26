# echomux Native App Spec

This document is a spec for developers building a native Android or iOS app that controls an echomux system. The full API reference is in **[ECHOMUX-API.md](ECHOMUX-API.md)**. The embedded web UI (Svelte SPA served at `/`) is the reference implementation to compare against.

---

## What the app controls

echomux is a headless Raspberry Pi service that streams audio to multiple Bluetooth speakers simultaneously. The app is a remote control: it connects to the echomux HTTP server over the local network and lets the user manage speakers, volume, mute, and per-speaker delay.

The app never plays audio itself. It only talks to the echomux REST API and WebSocket.

---

## Server discovery

echomux advertises itself via mDNS (Avahi/Bonjour), service type `_echomux._tcp`. The app should offer:

1. **Automatic discovery** via mDNS, listing found instances by name.
2. **Manual entry** of `host:port` as a fallback.

Default port is `56644`. HTTPS is optional (only if the user has configured TLS on the server). For most home setups, plain HTTP is fine.

Store the last-used server address so the app reconnects automatically on next launch.

---

## API basics

All endpoints are documented in ECHOMUX-API.md. Key points:

- Base URL: `http://<host>:<port>`
- All request and response bodies are JSON.
- No authentication. echomux is designed for trusted local networks.
- Most write endpoints return `204 No Content` on success.

---

## Multi-node routing

This is the most important invariant to get right.

`GET /devices` and `GET /nodes` are always called on the master (the server you connect to). In a multi-node setup, the device list includes both master-local speakers and satellite speakers. Each device has a `node_id` field.

`GET /nodes` returns the full node list. Find the master by `role == "master"` and note its `id`.

**Routing rule for all per-device API calls:**
- If `device.node_id == master.id`: call the endpoint directly, e.g. `PUT /devices/{mac}/volume`
- If `device.node_id != master.id`: prefix with `/nodes/{node_id}`, e.g. `PUT /nodes/fitness-room/devices/{mac}/volume`

The master proxies prefixed calls to the correct satellite. Calling a satellite device without the prefix (or the master device with the prefix) returns 404.

In single-node mode all devices have the master's `node_id` and all calls are direct.

---

## Core screens

### 1. Speaker list

The main screen. Shows all speakers grouped by node when multiple nodes exist, or as a flat list in single-node mode.

**Data:** `GET /devices` + `GET /nodes` on load. Refresh on WebSocket reconnect.

**Per-speaker card:**
- Speaker name and connection status (disconnected / connected / playing).
- Volume slider (0-100). Commit on release via `PUT /devices/{mac}/volume`.
- Mute toggle. Optimistic: apply locally, call `PUT /devices/{mac}/mute`, revert on failure.
- Delay chip showing current `delay_ms`. Tapping opens the delay screen.
- Connect / Disconnect button.

**Per-node header (multi-node):**
- Node name and role badge.
- Add speaker (+) button, opens the scan screen for that node.
- Restart button, calls `POST /playback/restart` for that node.

**Global header:**
- Global restart button: `POST /playback/restart` (no node prefix). Kills and respawns all loopbacks on all nodes. Wait ~2.5 s then refresh.

**Sort order:** connected speakers first, then alphabetical.

---

### 2. Scan / add speaker

Opens when the user taps the add button for a node.

**Flow:**
1. Record which speakers are currently connected (from local state).
2. Call `POST /scan` with `{ "timeout_sec": 10 }` (routed to the correct node).
3. Display discovered devices not already in the known speaker list.
4. User taps a device to add it: call `POST /devices/{mac}/pair` then `POST /devices/{mac}/connect` (both routed to the correct node).
5. Track per-device state: idle / pairing / connecting / done / error.
6. On close: call `POST /playback/resume` (correct node), then reconnect the speakers that were connected before the scan.

BT inquiry and A2DP streams share the same radio, so the server disconnects active speakers during scanning. The app must handle the reconnect-after-scan flow itself (the server does not auto-resume).

---

### 3. Delay adjustment

Per-speaker screen, opened from the delay chip on a speaker card.

- Slider: 0-2000 ms. Show the live value while dragging; commit on release via `PUT /devices/{mac}/delay`.
- Nudge buttons: -50, -10, +10, +50 ms.
- On API failure, revert the displayed value to the pre-commit value.
- Brief audio gap on the affected speaker when delay changes (the server kills and respawns the loopback). This is expected.

**Purpose of delay:** if a closer speaker reaches the listener before a more distant one, add delay to the closer speaker to align them in time. 1 ms roughly corresponds to 34 cm of distance.

---

## Real-time updates

Open a WebSocket to `ws://<host>:<port>/events` after the initial load. Keep it open; reconnect with backoff on close.

| Event | Action |
|---|---|
| `connected` | Mark speaker connected |
| `disconnected` | Mark speaker disconnected, clear playing state |
| `loopback_started` | Mark speaker playing |
| `loopback_stopped` | Clear playing state |
| `satellite_online` | Reload full device and node list |
| `satellite_offline` | Reload full device and node list |

Events carry a `mac` field (except satellite events which carry `node_id`). Match against the local device list by MAC. Ignore unknown MACs.

---

## Permissions

**Android:** `INTERNET`, `ACCESS_NETWORK_STATE`. If implementing mDNS discovery: `CHANGE_WIFI_MULTICAST_STATE`. No Bluetooth permissions needed (the app never talks to speakers directly).

**iOS:** Local network permission prompt required for mDNS and local HTTP. No Bluetooth entitlements needed.

---

## State the app must persist

- Last connected server (`host:port`).
- Optionally: node layout preferences (e.g. which node is expanded).

Speaker state (volume, mute, delay, connected status) is always fetched from the server and never stored locally.

---

## Error handling

| Scenario | Behaviour |
|---|---|
| Server unreachable | Show retry screen; do not cache stale device state |
| Connect fails with `org.bluez` in error body | Show "Connection failed, try moving closer" |
| Delay/volume call fails | Revert UI to previous value |
| Mute call fails | Revert toggle |
| Satellite offline | Show node as offline; disable per-node controls |
| WebSocket drops | Reconnect silently; reload state on reconnect |
