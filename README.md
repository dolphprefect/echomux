# echomux

[![CI](https://github.com/dolphprefect/echomux/actions/workflows/ci.yml/badge.svg)](https://github.com/dolphprefect/echomux/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/tag/dolphprefect/echomux?label=release&color=0ea5e9)](https://github.com/dolphprefect/echomux/releases/latest)
[![License](https://img.shields.io/badge/license-Elastic--2.0-2563eb)](LICENSE)

[Features](#features) | [Hardware](#hardware) | [Installation](#installation) | [Usage](#how-to-use) | [Multi-node](#multi-node-setup-satellite) | [Configuration](#configuration) | [License](#license)

Play Spotify on any number of Bluetooth speakers simultaneously, with per-speaker volume and latency adjustment for room alignment.

Open the web UI on your phone, connect your speakers, and pick **echomux** as the Spotify Connect source. Audio streams to all connected speakers at once.

---

## Features

- **Multi-room Bluetooth A2DP** - streams to any number of speakers simultaneously, any brand
- **Spotify Connect** - appears as a device in the Spotify app; no extra app needed
- **Per-speaker volume and mute**
- **Per-speaker delay** - compensate for room distance (0–2000 ms) to keep speakers in sync
- **Multi-node / satellite** - add more Raspberry Pis to reach speakers in distant rooms; all nodes are controlled from a single UI on the master
- **Mobile-first web UI** - phone-optimised, works in any browser; groups speakers by node when multiple nodes are active
- **Auto-reconnect** - known speakers reconnect automatically after a reboot
- **Headless** - no monitor, keyboard, or desktop environment required

---

## Hardware

**Tested on:**

- Raspberry Pi 5 (8 GB): master
- Raspberry Pi 4 (4 GB): satellite
- TP-Link UB500 USB Bluetooth 5.0 dongle (optional: better range than the built-in adapter)

**Should work on any:**

- Linux system with systemd and PipeWire
- Bluetooth adapter that supports A2DP source role (most do)

**Speakers:** any Bluetooth A2DP speaker

---

## Requirements

- Linux with systemd
- Bluetooth adapter (built-in or USB)
- Spotify Premium account (required by Spotify Connect / librespot)

---

## Installation

```bash
curl -fsSL https://raw.githubusercontent.com/dolphprefect/echomux/main/service/setup/install.sh | bash
```

The script is **interactive**, it will ask:

- **Mode**: `standalone` (single Pi), `master` (multi-room master), or `satellite`
- **Display name**: shown in the UI (defaults to hostname)
- **Bluetooth adapter**: which `hciN` adapter to use (auto-detected)
- **Master address** (satellite mode only): `host:port` of the master Pi
- **Spotify Connect name** (non-satellite only): how this Pi appears in the Spotify app

The script installs PipeWire, WirePlumber, BlueZ 5.85+, librespot (skipped for satellites), downloads the pre-built echomux binary from GitHub Releases, writes `/etc/echomux/echomux.conf`, and enables all systemd services.

**Reboot after install.** Bluetooth kernel state may be stale before the first reboot.

After reboot, open `http://<your-pi-ip>:56644` in a browser.

---

## How to use

1. Open `http://<your-pi-ip>:56644` on your phone
2. Tap **+** to scan for and add Bluetooth speakers
3. Open the Spotify app and select **echomux** as the playback device
4. Music plays on all connected speakers simultaneously

Each speaker card has an independent volume slider and a delay control. Use the delay to keep speakers in different rooms in sync, add delay to the closer speaker to match the travel time to the more distant one.

The **restart button** (↺) in the header kills and respawns all audio loopbacks on all nodes. Each node section also has its own restart button for per-node recovery.

---

## Multi-node setup (satellite)

A single Raspberry Pi can only reach speakers within Bluetooth range. To cover multiple rooms, run **satellite** echomux instances on additional Pis. Each satellite connects to the master over Wi-Fi, registers itself, and receives audio via RTP unicast. The master's web UI shows all nodes in grouped sections; speakers are managed on the node they belong to.

### Architecture

```
                    ┌─────────────────────────────┐
  Spotify app ──▶  │  MASTER echomux               │
  (Spotify Connect)│  - librespot + PipeWire        │
                   │  - web UI (:56644)             │
                   │  - REST API for all nodes      │
                   └──────────┬──────────────────┬─┘
                              │  RTP unicast      │  HTTP proxy
                     WebSocket│  (UDP 9001)       │  /nodes/{id}/...
                   ┌──────────▼──────────┐  ┌────▼──────────────────┐
                   │  SATELLITE echomux  │  │  SATELLITE echomux    │
                   │  living room        │  │  bedroom               │
                   │  BT speakers        │  │  BT speakers           │
                   └─────────────────────┘  └───────────────────────┘
```

The satellite is a full echomux instance running in `satellite` mode. It has no web UI of its own, the master proxies all API calls to it via `/nodes/{id}/...`.

### Setting up a satellite

**1. Install echomux on the satellite Pi** using the same install script:

```bash
curl -fsSL https://raw.githubusercontent.com/dolphprefect/echomux/main/service/setup/install.sh | bash
```

When prompted, select **satellite** mode and enter the master's `host:port`. The script writes `/etc/echomux/echomux.conf` and enables all services automatically. Reboot after install.

The satellite connects to the master via WebSocket, registers, and appears immediately in the UI under its own section.

---

## Configuration

All settings live in `/etc/echomux/echomux.conf`. Edit and restart to apply:

```bash
sudo systemctl restart echomux
```

| Variable | Default | Description |
|---|---|---|
| `ECHOMUX_ADAPTER` | `hci0` | Bluetooth adapter (`hciconfig -a` to list) |
| `ECHOMUX_ADDR` | `:56644` | HTTP listen address |
| `ECHOMUX_MODE` | `standalone` | Operating mode: `standalone`, `master`, or `satellite` |
| `ECHOMUX_NAME` | hostname | Node display name shown in the UI |
| `ECHOMUX_STATE_FILE` | `~/.local/share/echomux/state.json` | Where speaker settings are persisted |
| `ECHOMUX_MASTER_ADDR` | _(unset)_ | Master `host:port`, satellite mode only |
| `ECHOMUX_SELF_ADDR` | _(auto)_ | Public `host:port` this node reports to the master for HTTP proxying, satellite mode only; auto-detected if unset |
| `ECHOMUX_TLS_CERT` | _(unset)_ | TLS certificate path (enables HTTPS when set together with `ECHOMUX_TLS_KEY`) |
| `ECHOMUX_TLS_KEY` | _(unset)_ | TLS private key path |
| `ECHOMUX_DEBUG` | _(unset)_ | Set to `true` for verbose request/BT/audio logging |

The RTP port used for audio unicast to satellites is set with the CLI flag `--rtp-port` (default `9001`); it has no corresponding env var. It must match the port in the satellite's PipeWire RTP source config.

All settings can also be passed as CLI flags (flags take precedence over env vars). Run `echomux --help` for the full list.

---

## Service management

```bash
# Status of all services
systemctl status pipewire-system wireplumber-system echomux librespot --no-pager

# Live logs
journalctl -u echomux -f
journalctl -u librespot -f

# Restart everything (after a config change or audio graph problem)
sudo systemctl restart pipewire-system wireplumber-system librespot echomux
```

---

## External Bluetooth adapter (optional)

The Pi's built-in Bluetooth antenna is small. A USB dongle (e.g. TP-Link UB500) gives noticeably better range.

After plugging it in:

```bash
hciconfig -a               # confirm it's detected (usually hci1)
sudo rfkill unblock bluetooth
sudo hciconfig hci1 up
```

Set `ECHOMUX_ADAPTER=hci1` in `/etc/echomux/echomux.conf` and restart the service.

---

## Further reading

- **[ECHOMUX-SYSTEM.md](ECHOMUX-SYSTEM.md)** - architecture, PipeWire/BlueZ internals, troubleshooting, build instructions
- **[ECHOMUX-API.md](ECHOMUX-API.md)** - full HTTP and WebSocket API reference
- **[ECHOMUX-APP-SPEC.md](ECHOMUX-APP-SPEC.md)** - spec for building a native mobile app

---

## Support the project

echomux is free. If it's running in your home and your speakers are in sync, a coffee is always appreciated.

[buymeacoffee.com/dolphprefect](https://buymeacoffee.com/dolphprefect)

---

## License

Elastic License 2.0. Free to use, modify, and self-host. You may not offer echomux as a hosted or managed service to third parties. See [LICENSE](LICENSE).
