#!/bin/bash
# Runs librespot piped into pw-cat. Wrapper exists so PIPEWIRE_PROPS quoting
# is unambiguous (no shell escaping inside ExecStart).
export PIPEWIRE_RUNTIME_DIR=/run/pipewire
export PIPEWIRE_PROPS='{"node.dont-move":true}'
exec /usr/local/bin/librespot \
  --name "${ECHOMUX_SPOTIFY_NAME:-echomux}" \
  --backend pipe \
  --system-cache /var/cache/librespot \
  --initial-volume 100 \
  --bitrate 320 \
  --disable-audio-cache \
  | pw-cat --playback --raw \
      --target main-mix \
      --format s16 \
      --rate 44100 \
      --channels 2 -
