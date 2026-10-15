#!/bin/sh
# Install or update the speech server as a systemd service for the current user.
# Rerun after pulling to deploy a new server.py. Weights and venv are kept.
#   PATTER_DIR     where it lives            (default ~/patter)
#   PATTER_LISTEN  address to bind           (default 127.0.0.1)
#   TORCH_INDEX    PyTorch wheel index       (default cu124)
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
DIR=${PATTER_DIR:-$HOME/patter}
LISTEN=${PATTER_LISTEN:-127.0.0.1}
INDEX=${TORCH_INDEX:-https://download.pytorch.org/whl/cu124}
USER_NAME=$(id -un)
GROUP_NAME=$(id -gn)

mkdir -p "$DIR"
cp "$HERE/server.py" "$HERE/requirements.txt" "$DIR/"

if [ ! -x "$DIR/venv/bin/python" ]; then
    python3 -m venv "$DIR/venv"
fi
if ! "$DIR/venv/bin/python" -c 'import torch' 2>/dev/null; then
    "$DIR/venv/bin/python" -m pip install --quiet "$(grep '^torch==' "$HERE/requirements.txt")" --index-url "$INDEX"
fi
"$DIR/venv/bin/python" -m pip install --quiet -r "$DIR/requirements.txt"

if [ ! -f "$DIR/config.json" ]; then
    sed "s/\"127.0.0.1\"/\"$LISTEN\"/" "$HERE/server.example.json" > "$DIR/config.json"
fi
if [ ! -f "$DIR/glossary.txt" ]; then
    cp "$HERE/glossary.example.txt" "$DIR/glossary.txt"
fi
if [ ! -f "$DIR/token" ]; then
    umask 077
    "$DIR/venv/bin/python" -c 'import secrets; print(secrets.token_hex(32))' > "$DIR/token"
    umask 022
fi
chmod 600 "$DIR/token"

UNIT=/etc/systemd/system/patter.service
sed -e "s|^User=.*|User=$USER_NAME|" \
    -e "s|^Group=.*|Group=$GROUP_NAME|" \
    -e "s|/opt/patter|$DIR|g" \
    "$HERE/patter.service" > /tmp/patter.service.$$
if [ "$(id -u)" = 0 ]; then
    install -m 644 /tmp/patter.service.$$ "$UNIT"
    systemctl daemon-reload
    systemctl enable --now patter.service
    systemctl restart patter.service
else
    sudo install -m 644 /tmp/patter.service.$$ "$UNIT"
    sudo systemctl daemon-reload
    sudo systemctl enable --now patter.service
    sudo systemctl restart patter.service
fi
rm -f /tmp/patter.service.$$

echo "patter.service is up in $DIR"
echo "token: $DIR/token  (copy it to ~/.config/patter/token on each desktop)"
echo "health: http://$LISTEN:$(sed -n 's/.*"port": *\([0-9]*\).*/\1/p' "$DIR/config.json")/health"
