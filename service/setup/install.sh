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
# 0. Interactive setup
#    Reads from /dev/tty so it works even when the script is piped via curl.
#    Any parameter can be pre-set as an env var to skip the question.
# ---------------------------------------------------------------------------

_prompt() {
    local label="$1" default="$2" reply
    printf '%s' "$label" >/dev/tty
    IFS= read -r reply </dev/tty
    printf '%s' "${reply:-$default}"
}

_confirm() {
    local label="$1" default="${2:-y}" reply
    printf '%s' "$label" >/dev/tty
    IFS= read -r reply </dev/tty
    reply="${reply:-$default}"
    [[ "$reply" =~ ^[Yy] ]]
}

echo ""
echo "╔══════════════════════════════════════╗"
echo "║         echomux setup                ║"
echo "╚══════════════════════════════════════╝"
echo ""

# If a config already exists, offer to keep it.
REUSE_CONF=false
if [[ -f /etc/echomux/echomux.conf ]]; then
    if _confirm "    Existing config found. Keep it? [Y/n]: " "y"; then
        REUSE_CONF=true
    fi
fi

if [[ "$REUSE_CONF" == "true" ]]; then
    # Read existing values so the rest of the script has them.
    # shellcheck source=/dev/null
    source /etc/echomux/echomux.conf 2>/dev/null || true
    MODE="${ECHOMUX_MODE:-standalone}"
    echo "    Keeping existing config (mode: $MODE)."
else
    echo ""

    # --- Mode ---
    if [[ -n "${ECHOMUX_MODE:-}" ]]; then
        MODE="$ECHOMUX_MODE"
        echo "    Mode: $MODE (from env)"
    else
        echo "    Modes:"
        echo "      master     — receives Spotify Connect, streams audio to local and satellite speakers"
        echo "      satellite  — receives audio from master, drives local Bluetooth speakers"
        echo "      standalone — single-Pi setup (no satellites)"
        echo ""
        while true; do
            MODE=$(_prompt "    Mode [master/satellite/standalone] (standalone): " "standalone")
            [[ "$MODE" =~ ^(master|satellite|standalone)$ ]] && break
            echo "    Please enter master, satellite, or standalone." >/dev/tty
        done
    fi
    export ECHOMUX_MODE="$MODE"
    echo ""

    # --- Name ---
    HOSTNAME_DEFAULT=$(hostname -s 2>/dev/null || echo "echomux")
    if [[ -n "${ECHOMUX_NAME:-}" ]]; then
        echo "    Name: $ECHOMUX_NAME (from env)"
    else
        ECHOMUX_NAME=$(_prompt "    Display name [$HOSTNAME_DEFAULT]: " "$HOSTNAME_DEFAULT")
    fi
    echo ""

    # --- Bluetooth adapter ---
    if [[ -n "${ECHOMUX_ADAPTER:-}" ]]; then
        echo "    Adapter: $ECHOMUX_ADAPTER (from env)"
    else
        echo "    Available Bluetooth adapters:"
        _rfkill=$(rfkill list 2>/dev/null || true)
        for dev in /sys/class/bluetooth/hci*; do
            hci=$(basename "$dev")
            devtype=$(grep '^DEVTYPE=' "$dev/device/uevent" 2>/dev/null | cut -d= -f2 || true)
            modalias=$(cut -d: -f1 "$dev/device/modalias" 2>/dev/null || true)
            case "$devtype/$modalias" in
                usb_interface/*|*/usb) bus="USB" ;;
                */of|of/*)             bus="built-in" ;;
                */sdio|sdio/*)         bus="built-in (SDIO)" ;;
                */uart|uart/*)         bus="built-in (UART)" ;;
                *)                     bus="built-in" ;;
            esac
            blocked=$(printf '%s\n' "$_rfkill" | awk "/^[0-9]+: ${hci}:/{f=1} f && /Soft blocked: yes/{print \"soft-blocked\"; exit} f && /^[0-9]+: /{exit}")
            info="$bus"
            if [[ -n "$blocked" ]]; then info="$info, $blocked"; fi
            echo "      $hci  ($info)"
        done
        ADAPTER_DEFAULT=$(ls /sys/class/bluetooth/ 2>/dev/null | head -1 || echo "hci0")
        ECHOMUX_ADAPTER=$(_prompt "    Bluetooth adapter [$ADAPTER_DEFAULT]: " "$ADAPTER_DEFAULT")
    fi
    echo ""

    # --- Master address (satellite only) ---
    if [[ "$MODE" == "satellite" ]]; then
        if [[ -n "${ECHOMUX_MASTER_ADDR:-}" ]]; then
            echo "    Master address: $ECHOMUX_MASTER_ADDR (from env)"
        else
            while true; do
                ECHOMUX_MASTER_ADDR=$(_prompt "    Master address (host:port, e.g. 192.168.1.10:56644): " "")
                [[ -n "$ECHOMUX_MASTER_ADDR" ]] && break
                echo "    Master address is required for satellite mode." >/dev/tty
            done
        fi
        echo ""
    fi

    # --- Spotify Connect name (non-satellite only) ---
    if [[ "$MODE" != "satellite" ]]; then
        if [[ -n "${ECHOMUX_SPOTIFY_NAME:-}" ]]; then
            echo "    Spotify name: $ECHOMUX_SPOTIFY_NAME (from env)"
        else
            ECHOMUX_SPOTIFY_NAME=$(_prompt "    Spotify Connect name [echomux]: " "echomux")
        fi
        echo ""
    fi

    # --- Summary ---
    echo "    ── Configuration ──────────────────────"
    echo "    Mode:    $MODE"
    echo "    Name:    $ECHOMUX_NAME"
    echo "    Adapter: $ECHOMUX_ADAPTER"
    [[ "$MODE" == "satellite" ]] && echo "    Master:  $ECHOMUX_MASTER_ADDR"
    [[ "$MODE" != "satellite" ]] && echo "    Spotify: $ECHOMUX_SPOTIFY_NAME"
    echo "    ───────────────────────────────────────"
    echo ""
    if ! _confirm "    Proceed with installation? [Y/n]: " "y"; then
        echo "Aborted." >&2
        exit 1
    fi
    echo ""
fi

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
        if awk '/^Types: deb$/{f=1;next} f && /^Types: deb-src$/{exit 0} {f=0} END{exit 1}' \
               /etc/apt/sources.list.d/debian.sources; then
            echo "==> Fixing malformed debian.sources (duplicate Types lines)..."
            sed -i '/^Types: deb$/{N; s/^Types: deb\nTypes: deb-src$/Types: deb deb-src/}' \
                /etc/apt/sources.list.d/debian.sources
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

    BLUEZ_VER="5.86"
    echo "==> Building BlueZ ${BLUEZ_VER}..."
    TARBALL="bluez-${BLUEZ_VER}.tar.xz"
    curl -fL -o "/tmp/${TARBALL}" \
        "https://www.kernel.org/pub/linux/bluetooth/${TARBALL}"
    tar xf "/tmp/${TARBALL}" -C /tmp
    cd "/tmp/bluez-${BLUEZ_VER}"
    UDEVDIR=$(pkg-config --variable=udevdir udev 2>/dev/null || true)
    UDEVDIR=${UDEVDIR:-/usr/lib/udev}
    SYSTEMD_SYSTEM=$(pkg-config --variable=systemdsystemunitdir libsystemd 2>/dev/null || true)
    SYSTEMD_SYSTEM=${SYSTEMD_SYSTEM:-/usr/lib/systemd/system}
    SYSTEMD_USER=$(pkg-config --variable=systemduserunitdir libsystemd 2>/dev/null || true)
    SYSTEMD_USER=${SYSTEMD_USER:-/usr/lib/systemd/user}
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
# 8. echomux config directory and config file
# ---------------------------------------------------------------------------
echo "==> Writing /etc/echomux/echomux.conf..."
mkdir -p /etc/echomux

if [[ "$REUSE_CONF" != "true" ]]; then
    {
        echo "# echomux configuration"
        echo "# Restart the service after editing: sudo systemctl restart echomux"
        echo ""
        echo "ECHOMUX_MODE=${MODE}"
        echo "ECHOMUX_NAME=\"${ECHOMUX_NAME}\""
        echo "ECHOMUX_ADAPTER=${ECHOMUX_ADAPTER}"
        echo "ECHOMUX_ADDR=:56644"
        if [[ "$MODE" == "satellite" ]]; then
            echo "ECHOMUX_MASTER_ADDR=${ECHOMUX_MASTER_ADDR}"
        else
            echo "ECHOMUX_SPOTIFY_NAME=\"${ECHOMUX_SPOTIFY_NAME}\""
        fi
        echo ""
        echo "# Uncomment to enable verbose debug logging."
        echo "# ECHOMUX_DEBUG=true"
    } > /etc/echomux/echomux.conf
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
systemctl enable --now echomux.service
systemctl restart echomux.service

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

    echo "==> Creating librespot credential cache directory..."
    mkdir -p /var/cache/librespot
    chown "$SERVICE_USER:$SERVICE_USER" /var/cache/librespot

    echo "==> Installing librespot.service..."
    curl -fsSL "${RAW_BASE}/service/setup/librespot.service" \
        | sed "s/REPLACE_USER/$SERVICE_USER/" \
        > /etc/systemd/system/librespot.service
    systemctl daemon-reload
    systemctl enable --now librespot.service
    systemctl restart librespot.service
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
