#!/bin/sh
# First run seeds /data. Later runs keep what is there. PUID and PGID own
# /data and run the server, so the token is readable on the host.
set -eu
PUID=${PUID:-1000}
PGID=${PGID:-1000}
mkdir -p /data/hf
if [ ! -f /data/config.json ]; then
    sed -e 's/"127.0.0.1"/"0.0.0.0"/' \
        -e 's|"glossary.txt"|"/data/glossary.txt"|' \
        -e 's|"token"|"/data/token"|' \
        server.example.json > /data/config.json
fi
if [ ! -f /data/glossary.txt ]; then
    cp glossary.example.txt /data/glossary.txt
fi
if [ ! -f /data/token ]; then
    umask 077
    python -c 'import secrets; print(secrets.token_hex(32))' > /data/token
    echo "wrote a new token to /data/token"
fi
if [ "$(id -u)" = 0 ] && [ "$PUID" != 0 ]; then
    chown "$PUID:$PGID" /data /data/config.json /data/glossary.txt /data/token
    chown -R "$PUID:$PGID" /data/hf
    export HOME=/tmp
    exec setpriv --reuid="$PUID" --regid="$PGID" --clear-groups python server.py
fi
exec python server.py
