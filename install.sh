#!/bin/sh
# Build the desktop program and install it for the current user.
# Rerun to update. Config and token are kept.
#   PATTER_SERVER      speech server URL, written on first install (default http://127.0.0.1:8765)
#   PATTER_TOKEN_FILE  a copy of the server's token file, copied on first install
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
BIN=$HOME/.local/bin/patter
CONF=$HOME/.config/patter

cd "$HERE"
go build -o "$BIN.new" .
mkdir -p "$HOME/.local/bin" "$CONF"
mv -f "$BIN.new" "$BIN"

if [ ! -f "$CONF/config.json" ]; then
    sed "s|http://127.0.0.1:8765|${PATTER_SERVER:-http://127.0.0.1:8765}|" config.example.json > "$CONF/config.json"
fi
if [ ! -f "$CONF/token" ] && [ -n "${PATTER_TOKEN_FILE:-}" ]; then
    cp "$PATTER_TOKEN_FILE" "$CONF/token"
fi
[ -f "$CONF/token" ] && chmod 600 "$CONF/token"

for d in "$HOME/.local/share/applications" "$HOME/.config/autostart"; do
    mkdir -p "$d"
    sed "s|^Exec=.*|Exec=$BIN daemon|" patter.desktop > "$d/patter.desktop"
done

if [ -n "${DISPLAY:-}" ]; then
    "$BIN" status >/dev/null
fi

echo "installed $BIN"
[ -f "$CONF/token" ] || echo "put the server token in $CONF/token and chmod 600 it"
echo "settings: $CONF/config.json, or the Patter window"
