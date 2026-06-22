# echomux

Play Spotify on any number of Bluetooth speakers simultaneously, with per-speaker volume and latency adjustment for room alignment.

Open the web UI on your phone, connect your speakers, and pick **echomux** as the Spotify Connect source. Audio streams to all connected speakers at once.

---

## Features

- **Multi-room Bluetooth A2DP** — streams to any number of speakers simultaneously, any brand
- **Spotify Connect** — appears as a device in the Spotify app; no extra app needed
- **Per-speaker volume and mute**
- **Per-speaker delay** — compensate for room distance (0–2000 ms) to keep speakers in sync
- **Mobile-first web UI** — phone-optimised, works in any browser
- **Auto-reconnect** — known speakers reconnect automatically after a reboot
- **Headless** — no monitor, keyboard, or desktop environment required

---

## Hardware

**Tested on:**

- Raspberry Pi 5 (8 GB)
- Raspberry Pi OS Bookworm (64-bit, headless)
- TP-Link UB500 USB Bluetooth 5.0 dongle (optional — better range than the built-in adapter)

**Should work on any:**

- Linux machine running Debian Bookworm / Ubuntu 22.04+
- Bluetooth adapter that supports A2DP source role (most do)

**Speakers:** any Bluetooth A2DP speaker

---

## Requirements

- Linux with systemd
- Bluetooth adapter (built-in or USB)
- Go 1.21+ (for building)
- Spotify Premium account (required by Spotify Connect / librespot)
- `sudo` / root access for install

---

## Installation

```bash
git clone https://github.com/dolphprefect/echomux-pi
cd echomux-pi
sudo ./service/setup/install.sh
```

The script installs PipeWire, WirePlumber, BlueZ 5.85+, librespot, builds the echomux binary, and enables all systemd services.

**Reboot after install.** Bluetooth kernel state may be stale before the first reboot.

After reboot, open `http://<your-pi-ip>:56644` in a browser.

---

## How to use

1. Open `http://<your-pi-ip>:56644` on your phone
2. Tap **+** to scan for and add Bluetooth speakers
3. Open the Spotify app and select **echomux** as the playback device
4. Music plays on all connected speakers simultaneously

Each speaker card has an independent volume slider and a delay control. Use the delay to keep speakers in different rooms in sync — add delay to the closer speaker to match the travel time to the more distant one.

The **restart button** (↺) in the header kills and respawns all audio loopbacks. Use it if speakers are connected but silent.

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
| `ECHOMUX_SPOTIFY_NAME` | `echomux` | Name shown in the Spotify source picker |
| `ECHOMUX_STATE_FILE` | `~/.local/share/echomux/state.json` | Where speaker settings are persisted |
| `ECHOMUX_DEBUG` | _(unset)_ | Set to `true` for verbose logging |

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

- **[ECHOMUX-SYSTEM.md](ECHOMUX-SYSTEM.md)** — architecture, PipeWire/BlueZ internals, troubleshooting, build instructions
- **[ECHOMUX-API.md](ECHOMUX-API.md)** — full HTTP and WebSocket API reference
- **[ECHOMUX-APP-SPEC.md](ECHOMUX-APP-SPEC.md)** — spec for building a native mobile app

---

## Support the project

echomux is free, open-source software. If it brings music to your home, a coffee goes a long way:

[☕ buymeacoffee.com/dolphprefect](https://buymeacoffee.com/dolphprefect)

---

## License

MIT
