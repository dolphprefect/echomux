# echomux Native App — Agent Spec

This document is written for an AI coding agent tasked with building a native mobile app (Android or iOS) for echomux. It covers what echomux is, how to discover it on the network, the UI the app should implement, and how to handle state and errors.

The full API contract is in **[ECHOMUX-API.md](ECHOMUX-API.md)**.

---

## What echomux is

echomux is a Raspberry Pi (or any Linux box) that plays Spotify simultaneously on any number of Bluetooth A2DP speakers. The user opens Spotify on their phone, selects "echomux" as the Connect source, and audio flows to all paired speakers at once. The native app manages the speakers: connect, disconnect, volume, mute, and per-speaker delay for room alignment.

The backend exposes a plain HTTP + WebSocket API on the local network. There is no cloud, no auth, no pairing between the app and the server — the app talks directly to the Pi's IP.

---

## Discovery

echomux advertises itself over mDNS (Bonjour/Avahi) as:

```
Service type: _echomux._tcp
Default port: 56644
TXT records:
  api=v1
  rtp_port=9001
  rtp_codec=L16/48000/2
```

Use mDNS to discover the service automatically. On Android use `NsdManager`; on iOS use `NetServiceBrowser`. The user should not need to enter an IP address manually, though a manual fallback field is a nice-to-have.

The host can also be reached at `echomux.local` if mDNS resolution works on the device.

---

## Base URL

```
http://<discovered-host>:56644
```

No TLS, no auth. See [ECHOMUX-API.md](ECHOMUX-API.md) for the full endpoint reference.

---

## UI the app should implement

### Speaker list (main screen)

- Shows all known speakers from `GET /devices`
- Each speaker card shows: name, connection status indicator, volume slider, mute toggle
- Status indicator has three states: **playing** (audio flowing), **connected** (BT up, loopback not yet running), **disconnected**
- Tapping a disconnected speaker connects it; tapping a connected speaker disconnects it
- Volume slider sends updates as the user drags (debounce ~200 ms); fire-and-forget, no revert on failure
- A **restart** button (⟳) kills and respawns all loopbacks — use when speakers are connected but silent

### Add speaker flow

1. User taps **+**
2. App calls `POST /scan` with `timeout_sec: 10`
3. Show a scanning indicator; display discovered devices when the scan completes
4. Filter out MACs already in the known speaker list (`GET /devices`)
5. User taps a device → app calls `POST /devices/:mac/pair` then `POST /devices/:mac/connect`
6. On success: close the scan sheet, call `POST /playback/resume`, reconnect any speakers that were disconnected for the scan
7. On error: show the error inline and allow retry

### Delay adjustment

- Per-speaker delay slider (0–2000 ms) on a detail screen or slide-out panel
- Show the current value in ms with ±10 and ±50 ms nudge buttons for fine adjustment
- Warn the user that changing the delay causes a brief audio gap on that speaker only
- Call `PUT /devices/:mac/delay` on commit (not on every drag tick)

### Forget speaker

- Accessible from the speaker card or a detail screen
- Confirm before proceeding: "Forget [Name]? This will unpair the speaker from this device."
- Call `DELETE /devices/:mac` on confirm
- Remove the speaker from the local list immediately (optimistic)

---

## State management

| Event | Action |
|---|---|
| App launch | Connect WebSocket → fetch `GET /devices` → render speaker list |
| `connected` WS event | Mark that speaker as Connected in local model |
| `disconnected` WS event | Mark as disconnected; clear Playing |
| `loopback_started` WS event | Mark as Playing |
| `loopback_stopped` WS event | Clear Playing |
| WebSocket close | Fall back to polling `GET /devices` every 5 s; reconnect WS with exponential backoff |
| Connect tap | Show loading spinner until `loopback_started` arrives or 15 s timeout |
| Volume/mute change | Optimistic UI update; fire API call; volume reverts on failure only for mute (volume is fire-and-forget) |
| Poll response | Normalise `muted`/`playing` fields — the API returns lowercase; keep local state consistent |

---

## Error handling

- If the host is unreachable: show a "can't reach echomux" screen with a **Try again** button and a manual IP/hostname entry field
- API errors return plain text with HTTP status codes; surface these as inline error messages on the relevant control
- `POST /devices/:mac/pair` returning 404 means the device disappeared after the scan — prompt the user to put it back in pairing mode and retry
- `POST /devices/:mac/connect` returning 404 means the device is not in BlueZ — prompt to scan again
- Never block the UI for more than 15 s waiting for async confirmation; show a timeout message and let the user retry

---

## What the app does NOT need to do

- No auth or login
- No cloud sync
- No push notifications
- No playback control (play / pause / skip) — that is handled in the Spotify app
- No audio streaming from the phone — echomux receives audio via Spotify Connect on the Pi
- No settings persistence on the app side — all state (volumes, delays, known speakers) lives on the server
