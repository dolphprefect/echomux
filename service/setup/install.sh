#!/usr/bin/env bash
# install.sh — bootstrap echomux on a fresh Raspberry Pi OS (or Debian-based) install
# Run:  curl -fsSL https://raw.githubusercontent.com/dolphprefect/echomux/main/service/setup/install.sh | bash
# Or with a custom service user:
#   curl -fsSL https://raw.githubusercontent.com/dolphprefect/echomux/main/service/setup/install.sh | SERVICE_USER=myuser bash
set -euo pipefail

# Files below are fetched from the main branch of this repo.
RAW_BASE="https://raw.githubusercontent.com/dolphprefect/echomux/main"

# Re-exec with sudo if not already root (so the one-liner works without | sudo).
if [[ $EUID -ne 0 ]]; then
    exec sudo bash -c "$(curl -fsSL "${RAW_BASE}/service/setup/install.sh")" "$@"
fi

# Binary is downloaded from GitHub Releases.
GITHUB_REPO="dolphprefect/echomux"
# Override ECHOMUX_VERSION to pin a specific release tag (e.g. v1.2.0).
ECHOMUX_VERSION="${ECHOMUX_VERSION:-latest}"

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
    pipewire-bin \
    wireplumber \
    pipewire-pulse \
    libspa-0.2-bluetooth \
    curl \
    avahi-daemon \
    rfkill
# pipewire-bin: pw-loopback (one per speaker), pw-link (main-mix wiring), pw-dump (snapshot)
# wireplumber:  wpctl (volume/mute control)
# libspa-0.2-bluetooth: PipeWire Bluetooth SPA plugin (A2DP codec negotiation)

BLUEZ_MIN="5.83"
BLUEZ_GOT=$(bluetoothd --version 2>/dev/null || echo "0")
if printf '%s\n%s\n' "$BLUEZ_MIN" "$BLUEZ_GOT" | sort -V -C; then
    echo "==> BlueZ $BLUEZ_GOT OK (>= $BLUEZ_MIN), skipping upgrade."
else
    echo "==> BlueZ $BLUEZ_GOT is too old (< $BLUEZ_MIN). Building latest BlueZ from source..."

    # Pi OS ships debian.sources with separate "Types: deb" and "Types: deb-src"
    # lines in the same Deb822 stanza. In that format duplicate keys overwrite,
    # so deb-src wins and apt only fetches source indexes — no binary packages.
    # Fix it to "Types: deb deb-src" before anything else.
    if [ -f /etc/apt/sources.list.d/debian.sources ]; then
        if grep -q $'^Types: deb\nTypes: deb-src' /etc/apt/sources.list.d/debian.sources 2>/dev/null || \
           python3 -c "
import sys
t = open('/etc/apt/sources.list.d/debian.sources').read()
sys.exit(0 if 'Types: deb\nTypes: deb-src' in t else 1)
" 2>/dev/null; then
            echo "==> Fixing malformed debian.sources (duplicate Types lines)..."
            python3 -c "
t = open('/etc/apt/sources.list.d/debian.sources').read()
t = t.replace('Types: deb\nTypes: deb-src', 'Types: deb deb-src')
open('/etc/apt/sources.list.d/debian.sources', 'w').write(t)
"
            apt-get update -qq
        fi
    fi

    # Install compile-time deps directly rather than via 'apt-get build-dep bluez'.
    # build-dep pulls in Debian packaging tools (debhelper-compat, check, python3-docutils)
    # that are often uninstallable on Raspberry Pi OS.
    apt-get install -y --no-install-recommends \
        build-essential pkg-config autoconf automake libtool \
        flex bison libdbus-1-dev libglib2.0-dev libudev-dev \
        libreadline-dev libical-dev libjson-c-dev libdw-dev libell-dev

    BLUEZ_VER=$(curl -fsSL https://api.github.com/repos/bluez/bluez/releases/latest \
        | grep -oP '"tag_name":\s*"\K[^"]+' \
        | sed 's/^v//')
    echo "==> Building BlueZ ${BLUEZ_VER}..."
    TARBALL="bluez-${BLUEZ_VER}.tar.xz"
    curl -fL -o "/tmp/${TARBALL}" \
        "https://www.kernel.org/pub/linux/bluetooth/${TARBALL}"
    tar xf "/tmp/${TARBALL}" -C /tmp
    cd "/tmp/bluez-${BLUEZ_VER}"
    UDEVDIR=$(pkg-config --variable=udevdir udev 2>/dev/null || echo /usr/lib/udev)
    SYSTEMD_SYSTEM=$(pkg-config --variable=systemdsystemunitdir libsystemd 2>/dev/null || echo /usr/lib/systemd/system)
    SYSTEMD_USER=$(pkg-config --variable=systemduserunitdir libsystemd 2>/dev/null || echo /usr/lib/systemd/user)
    ./configure --prefix=/usr --sysconfdir=/etc --localstatedir=/var \
        --with-udevdir="${UDEVDIR}" \
        --with-systemdsystemunitdir="${SYSTEMD_SYSTEM}" \
        --with-systemduserunitdir="${SYSTEMD_USER}" \
        --disable-manpages --disable-testing
    make -j"$(nproc)"
    make install
    cd /
    rm -rf "/tmp/bluez-${BLUEZ_VER}" "/tmp/${TARBALL}"

    BLUEZ_GOT=$(bluetoothd --version 2>/dev/null || echo "0")
    if ! printf '%s\n%s\n' "$BLUEZ_MIN" "$BLUEZ_GOT" | sort -V -C; then
        echo "ERROR: BlueZ $BLUEZ_GOT still below minimum $BLUEZ_MIN." >&2
        echo "       Simultaneous A2DP to multiple sinks will not work." >&2
        exit 1
    fi
    echo "==> BlueZ $BLUEZ_GOT OK (>= $BLUEZ_MIN)"
fi

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
curl -fLSo /etc/wireplumber/wireplumber.conf.d/50-systemwide.conf \
    "${RAW_BASE}/service/setup/50-systemwide.conf"
curl -fLSo /etc/wireplumber/wireplumber.conf.d/51-bluetooth-roles.conf \
    "${RAW_BASE}/service/setup/51-bluetooth-roles.conf"
curl -fLSo /etc/wireplumber/wireplumber.conf.d/52-bt-isolation.conf \
    "${RAW_BASE}/service/setup/52-bt-isolation.conf"

# ---------------------------------------------------------------------------
# 3b. PipeWire: virtual main-mix sink + RTP source module
# ---------------------------------------------------------------------------
echo "==> Writing PipeWire config..."
mkdir -p /etc/pipewire/pipewire.conf.d
curl -fLSo /etc/pipewire/pipewire.conf.d/10-rtp-source.conf \
    "${RAW_BASE}/service/setup/10-rtp-source.conf"
curl -fLSo /etc/pipewire/pipewire.conf.d/20-main-mix-loopback.conf \
    "${RAW_BASE}/service/setup/20-main-mix-loopback.conf"
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

cat > /etc/systemd/system/pipewire-pulse-system.socket << EOF
[Unit]
Description=PipeWire PulseAudio Compatibility System Socket

[Socket]
ListenStream=/run/pipewire/pulse-native
DirectoryMode=0755

[Install]
WantedBy=sockets.target
EOF

cat > /etc/systemd/system/pipewire-pulse-system.service << EOF
[Unit]
Description=PipeWire PulseAudio Compatibility
After=pipewire-system.service
Requires=pipewire-system.service pipewire-pulse-system.socket

[Service]
User=$SERVICE_USER
Group=audio
Environment=PIPEWIRE_RUNTIME_DIR=/run/pipewire
Environment=PULSE_RUNTIME_PATH=/run/pipewire
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
systemctl enable --now pipewire-pulse-system.socket
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

# Uncomment to enable verbose debug logging.
# ECHOMUX_DEBUG=true
CONF
fi

# ---------------------------------------------------------------------------
# 9. Download pre-built echomux binary from GitHub Releases
# ---------------------------------------------------------------------------
echo "==> Downloading echomux binary (${ECHOMUX_VERSION})..."
if [[ "$ECHOMUX_VERSION" == "latest" ]]; then
    DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/latest/download/echomux-linux-arm64"
else
    DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/download/${ECHOMUX_VERSION}/echomux-linux-arm64"
fi
curl -fL -o /usr/local/bin/echomux "${DOWNLOAD_URL}"
chmod +x /usr/local/bin/echomux

# ---------------------------------------------------------------------------
# 10. Install and enable the echomux systemd service
# ---------------------------------------------------------------------------
echo "==> Installing echomux.service..."
curl -fsSL "${RAW_BASE}/service/echomux.service" \
    | sed "s/REPLACE_USER/$SERVICE_USER/" \
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
curl -fLSo /etc/avahi/services/echomux.service \
    "${RAW_BASE}/service/setup/echomux-avahi.service"
systemctl enable --now avahi-daemon

# ---------------------------------------------------------------------------
# 13. Librespot (Spotify Connect) — pre-built binary from raspotify release
#     Compiling from source fails on Raspberry Pi OS when libc6 is newer than
#     the available libc6-dev. raspotify ships a pre-built ARM64 librespot
#     binary that works on all supported hardware.
# ---------------------------------------------------------------------------
# Detect mode from environment or existing config file
MODE="${ECHOMUX_MODE:-}"
if [[ -z "$MODE" && -f /etc/echomux/echomux.conf ]]; then
    MODE=$(grep -oP '^[^#]*ECHOMUX_MODE=\s*\K[a-zA-Z0-9_-]+' /etc/echomux/echomux.conf || echo "")
fi

if [[ "$MODE" == "satellite" ]]; then
    echo "==> Satellite mode detected ($MODE). Skipping librespot installation."
    if systemctl is-enabled librespot.service &>/dev/null || systemctl is-active librespot.service &>/dev/null; then
        echo "==> Stopping and disabling librespot.service..."
        systemctl disable --now librespot.service || true
    fi
else
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
    curl -fsSL "${RAW_BASE}/service/setup/librespot-pipewire.sh" \
        > /usr/local/bin/librespot-pipewire.sh
    chmod +x /usr/local/bin/librespot-pipewire.sh

    echo "==> Installing librespot.service..."
    curl -fsSL "${RAW_BASE}/service/setup/librespot.service" \
        | sed "s/REPLACE_USER/$SERVICE_USER/" \
        > /etc/systemd/system/librespot.service
    systemctl daemon-reload
    systemctl enable --now librespot.service
fi

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
if [[ "$MODE" == "satellite" ]]; then
    echo "      systemctl status pipewire-system wireplumber-system echomux"
else
    echo "      systemctl status pipewire-system wireplumber-system echomux librespot"
fi
echo "      PIPEWIRE_RUNTIME_DIR=/run/pipewire wpctl status"
