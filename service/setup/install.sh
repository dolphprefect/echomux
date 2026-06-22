#!/usr/bin/env bash
# install.sh — bootstrap echomux on a fresh Raspberry Pi OS (or Debian-based) install
# Run as root:  sudo ./service/setup/install.sh
# Or specify a different service user:  sudo SERVICE_USER=myuser ./pi/setup/install.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PI_DIR="$REPO_ROOT/pi"

# ---------------------------------------------------------------------------
# Determine which user will own the audio services and run echomux
# ---------------------------------------------------------------------------
SERVICE_USER="${SERVICE_USER:-${SUDO_USER:-}}"
if [[ -z "$SERVICE_USER" ]]; then
    echo "ERROR: Could not determine SERVICE_USER. Run via sudo or set SERVICE_USER=<user>." >&2
    exit 1
fi
echo "==> Installing for user: $SERVICE_USER"

# ---------------------------------------------------------------------------
# 1. System packages
# ---------------------------------------------------------------------------
echo "==> Installing system packages..."
apt-get update -qq
apt-get install -y --no-install-recommends \
    pipewire \
    wireplumber \
    pipewire-pulse \
    libspa-0.2-bluetooth \
    golang-go \
    git \
    libbluetooth-dev \
    build-essential \
    avahi-daemon \
    rfkill

# BlueZ 5.83+ fixes simultaneous A2DP connections to multiple sinks (commit 05f8bd4).
# Debian Trixie ships 5.82 which has the bug; install 5.85 from Sid pinned just for bluez.
echo "==> Installing BlueZ 5.85+ (multi-sink A2DP fix)..."
cat > /etc/apt/sources.list.d/sid-bluez.list << 'EOF'
deb http://deb.debian.org/debian sid main
EOF
cat > /etc/apt/preferences.d/sid-bluez-pin << 'EOF'
Package: *
Pin: release a=sid
Pin-Priority: -1

Package: bluez
Pin: release a=sid
Pin-Priority: 900
EOF
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends -o Dpkg::Options::="--force-confold" bluez
rm /etc/apt/sources.list.d/sid-bluez.list /etc/apt/preferences.d/sid-bluez-pin
apt-get update -qq

# ---------------------------------------------------------------------------
# 1b. Unblock Bluetooth (rfkill soft-block is the most common reason a USB BT
#     dongle silently fails at boot — rfkill unblock is safe and idempotent)
# ---------------------------------------------------------------------------
echo "==> Unblocking Bluetooth adapters via rfkill..."
rfkill unblock bluetooth || true

# ---------------------------------------------------------------------------
# 1c. Radio interference note (logged, not automated — physical/network config)
# ---------------------------------------------------------------------------
# USB 3.0 devices radiate ~2.4 GHz noise that degrades Bluetooth link quality.
# To reduce interference:
#   • Prefer 5 GHz Wi-Fi (not 2.4 GHz) on this machine and nearby APs.
#   • Position USB 3.0 SSDs / hubs away from the Bluetooth antenna / dongle.
#   • Use a short USB 3.0 extension cable to move the dongle away from the Pi.
# These are physical mitigations; no kernel config reliably eliminates the RF
# coupling on Pi hardware.

# ---------------------------------------------------------------------------
# 2. User groups (audio + bluetooth access without sudo)
# ---------------------------------------------------------------------------
echo "==> Adding $SERVICE_USER to audio and bluetooth groups..."
usermod -aG audio,bluetooth "$SERVICE_USER"

# ---------------------------------------------------------------------------
# 3. WirePlumber: headless config + BT roles (a2dp_source only — host sends
#    audio to speakers; phones stream via Spotify Connect, not BT A2DP sink)
# ---------------------------------------------------------------------------
echo "==> Writing WirePlumber headless config..."
mkdir -p /etc/wireplumber/wireplumber.conf.d
cp "$PI_DIR/setup/50-systemwide.conf" \
   /etc/wireplumber/wireplumber.conf.d/50-systemwide.conf
cp "$PI_DIR/setup/51-bluetooth-roles.conf" \
   /etc/wireplumber/wireplumber.conf.d/51-bluetooth-roles.conf
cp "$PI_DIR/setup/52-bt-isolation.conf" \
   /etc/wireplumber/wireplumber.conf.d/52-bt-isolation.conf

# ---------------------------------------------------------------------------
# 3b. PipeWire: virtual main-mix sink + RTP source module
# ---------------------------------------------------------------------------
echo "==> Writing PipeWire config..."
mkdir -p /etc/pipewire/pipewire.conf.d
cp "$PI_DIR/setup/10-rtp-source.conf" \
   /etc/pipewire/pipewire.conf.d/10-rtp-source.conf
cp "$PI_DIR/setup/20-main-mix-loopback.conf" \
   /etc/pipewire/pipewire.conf.d/20-main-mix-loopback.conf
# Remove old loopback config if present from a previous install.
rm -f /etc/pipewire/pipewire.conf.d/20-librespot-loopback.conf

# ---------------------------------------------------------------------------
# 4. System-level PipeWire / WirePlumber / PulseAudio services
#    These run at boot under SERVICE_USER without any login session.
# ---------------------------------------------------------------------------
echo "==> Writing system-level PipeWire service units..."

cat > /etc/systemd/system/pipewire-system.service << EOF
[Unit]
Description=PipeWire Sound System
After=dbus.service bluetooth.service
Wants=dbus.service

[Service]
User=$SERVICE_USER
Group=audio
Environment=PIPEWIRE_RUNTIME_DIR=/run/pipewire
RuntimeDirectory=pipewire
RuntimeDirectoryMode=0755
ExecStart=/usr/bin/pipewire
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF

cat > /etc/systemd/system/wireplumber-system.service << EOF
[Unit]
Description=WirePlumber Session Manager
After=pipewire-system.service bluetooth.service
Requires=pipewire-system.service
Wants=bluetooth.service

[Service]
User=$SERVICE_USER
Group=audio
Environment=PIPEWIRE_RUNTIME_DIR=/run/pipewire
ExecStart=/usr/bin/wireplumber
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF

cat > /etc/systemd/system/pipewire-pulse-system.service << EOF
[Unit]
Description=PipeWire PulseAudio Compatibility
After=pipewire-system.service
Requires=pipewire-system.service

[Service]
User=$SERVICE_USER
Group=audio
Environment=PIPEWIRE_RUNTIME_DIR=/run/pipewire
ExecStart=/usr/bin/pipewire-pulse
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF

# ---------------------------------------------------------------------------
# 5. Disable the user-session pipewire socket units (they fight the above)
# ---------------------------------------------------------------------------
echo "==> Masking user-session PipeWire socket units..."
systemctl mask pipewire.socket pipewire-pulse.socket 2>/dev/null || true

# ---------------------------------------------------------------------------
# 6. Global environment: point all tools at the system PipeWire socket
# ---------------------------------------------------------------------------
echo "==> Setting PIPEWIRE_RUNTIME_DIR globally..."
grep -qxF 'PIPEWIRE_RUNTIME_DIR=/run/pipewire' /etc/environment \
    || echo 'PIPEWIRE_RUNTIME_DIR=/run/pipewire' >> /etc/environment
echo 'PIPEWIRE_RUNTIME_DIR=/run/pipewire' > /etc/profile.d/pipewire-runtime.sh
chmod +x /etc/profile.d/pipewire-runtime.sh

# ---------------------------------------------------------------------------
# 7. Enable and start system audio services
# ---------------------------------------------------------------------------
echo "==> Enabling and starting system audio services..."
systemctl daemon-reload
systemctl enable --now pipewire-system.service
systemctl enable --now wireplumber-system.service
systemctl enable --now pipewire-pulse-system.service

# ---------------------------------------------------------------------------
# 8. echomux config directory and default config file
# ---------------------------------------------------------------------------
echo "==> Writing /etc/echomux/echomux.conf..."
mkdir -p /etc/echomux

# Only write the default config if it doesn't already exist (preserve customisations).
if [[ ! -f /etc/echomux/echomux.conf ]]; then
    cat > /etc/echomux/echomux.conf << 'CONF'
# echomux configuration
# Restart the service after editing: sudo systemctl restart echomux

# Bluetooth adapter to use. Run 'hciconfig -a' to list available adapters.
# On Raspberry Pi, hci0 is the built-in adapter. An external USB dongle
# typically appears as hci1.
ECHOMUX_ADAPTER=hci0

# HTTP/HTTPS listen address.
ECHOMUX_ADDR=:56644

# Spotify Connect device name — shown in the Spotify app source picker.
ECHOMUX_SPOTIFY_NAME=echomux

# TLS — comment out to use plain HTTP.
# Enabling HTTPS is required for the PWA install on Android (chromeless mode).
# See README.md for mkcert instructions.
# ECHOMUX_TLS_CERT=/etc/echomux/tls/cert.pem
# ECHOMUX_TLS_KEY=/etc/echomux/tls/key.pem
# ECHOMUX_TLS_CA=/etc/echomux/tls/ca.pem

# Uncomment to enable verbose debug logging.
# ECHOMUX_DEBUG=true
CONF
fi

# ---------------------------------------------------------------------------
# 9. Build echomux binary
# ---------------------------------------------------------------------------
echo "==> Building echomux..."
cd "$PI_DIR"
go build -o /usr/local/bin/echomux ./cmd/echomux

# ---------------------------------------------------------------------------
# 10. Install and enable the echomux systemd service
# ---------------------------------------------------------------------------
echo "==> Installing echomux.service..."
sed "s/REPLACE_USER/$SERVICE_USER/" "$PI_DIR/echomux.service" \
    > /etc/systemd/system/echomux.service
systemctl daemon-reload
systemctl enable echomux.service

# ---------------------------------------------------------------------------
# 11. Bluetooth main.conf: disable Pi-side reconnect (speakers reconnect to us)
# ---------------------------------------------------------------------------
echo "==> Configuring BlueZ policy..."
if ! grep -q 'ReconnectUUIDs=' /etc/bluetooth/main.conf 2>/dev/null; then
    printf '\n[Policy]\nReconnectUUIDs=\n' >> /etc/bluetooth/main.conf
fi

# ---------------------------------------------------------------------------
# 12. mDNS advertisement via avahi
# ---------------------------------------------------------------------------
echo "==> Installing mDNS service advertisement..."
mkdir -p /etc/avahi/services
cp "$PI_DIR/setup/echomux-avahi.service" /etc/avahi/services/echomux.service
systemctl enable --now avahi-daemon

# ---------------------------------------------------------------------------
# 13. Librespot (Spotify Connect) — pre-built binary from raspotify release
#     Compiling from source fails on Raspberry Pi OS when libc6 is newer than
#     the available libc6-dev. raspotify ships a pre-built ARM64 librespot
#     binary that works on all supported hardware.
# ---------------------------------------------------------------------------
RASPOTIFY_VER="0.48.1"
LIBRESPOT_VER="v0.8.0-ea81314"
RASPOTIFY_DEB="raspotify_${RASPOTIFY_VER}.librespot.${LIBRESPOT_VER}_arm64.deb"
RASPOTIFY_URL="https://github.com/dtcooper/raspotify/releases/download/${RASPOTIFY_VER}/${RASPOTIFY_DEB}"

echo "==> Downloading pre-built librespot from raspotify ${RASPOTIFY_VER}..."
curl -L -o "/tmp/${RASPOTIFY_DEB}" "${RASPOTIFY_URL}"
dpkg-deb -x "/tmp/${RASPOTIFY_DEB}" /tmp/raspotify-extract
install -m 755 /tmp/raspotify-extract/usr/bin/librespot /usr/local/bin/librespot
rm -rf "/tmp/${RASPOTIFY_DEB}" /tmp/raspotify-extract

echo "==> Installing librespot-pipewire.sh wrapper..."
install -m 755 "$PI_DIR/setup/librespot-pipewire.sh" /usr/local/bin/librespot-pipewire.sh

echo "==> Installing librespot.service..."
sed "s/REPLACE_USER/$SERVICE_USER/" "$PI_DIR/setup/librespot.service" \
    > /etc/systemd/system/librespot.service
systemctl daemon-reload
systemctl enable --now librespot.service

# ---------------------------------------------------------------------------
# 14. Clean up stale WirePlumber stream-properties
# ---------------------------------------------------------------------------
echo "==> Cleaning stale WirePlumber stream state..."
rm -f "/home/$SERVICE_USER/.local/state/wireplumber/stream-properties"
systemctl restart wireplumber-system || true

echo ""
echo "==> Done. Reboot recommended to clear any stale Bluetooth kernel state."
echo "    After reboot, echomux starts automatically."
echo ""
echo "    Config file:  /etc/echomux/echomux.conf"
echo "    To check status:"
echo "      systemctl status pipewire-system wireplumber-system echomux librespot"
echo "      PIPEWIRE_RUNTIME_DIR=/run/pipewire wpctl status"
