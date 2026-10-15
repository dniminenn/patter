# Patter

Hold a key and talk. Words land in the focused window while the key is down. Release the key and a second model cleans that line: fillers drop, a spoken correction replaces the words it retracts, punctuation and capitals go in, and names in your glossary keep the spelling you wrote. The sentence stays what you said.

## Two programs

The desktop program is Go. It listens for the key, reads the microphone, and types.

`server.py` is Python because the models are PyTorch. Run it on the same machine, or on another machine that has the GPU.

The desktop program talks to the URL in its config. `http://127.0.0.1:8765` means the GPU is this machine. Any other `http://` URL means the audio goes to that machine, and only while the key is held.

A long hold is cut into 5 second pieces. Each piece is one pass through the speech model. Finished pieces stay as text, so a long talk does not feed a growing recording back into the GPU. The cleanup pass runs on that text when you let go.

## Speech server

On the machine with the GPU, clone this repo and run:

```
PATTER_LISTEN=0.0.0.0 ./install-server.sh
```

It makes `~/patter` with a venv, installs the CUDA 12.4 PyTorch wheel and `requirements.txt`, writes `config.json`, `glossary.txt`, and a random `token`, and starts `patter.service` as you. Rerun it after a pull to deploy a new `server.py`. The venv and the weights stay.

`PATTER_DIR` picks another directory. `TORCH_INDEX` picks another PyTorch wheel index when the driver needs one. Leave `PATTER_LISTEN` unset when the GPU is in the machine you type on, and the server binds `127.0.0.1` only.

The first start downloads the models. `GET /health` returns `{"ready": true}` when both are loaded, and `state`, `model`, and `percent` while they are coming down.

### Or in a container

With Docker and the NVIDIA Container Toolkit:

```
docker compose up -d
```

It builds the image on the official PyTorch CUDA 12.4 base, keeps `config.json`, `glossary.txt`, `token`, and the weights in `./data`, and binds `127.0.0.1:8765`. Edit the `ports` line in `compose.yaml` to let other machines use the GPU. The token is written on first start to `data/token`. Files in `data/` belong to `PUID:PGID`, 1000 by default; set those to your own ids in the environment or a `.env` file.

With Podman and CDI:

```
podman build -t patter-server .
podman run -d --name patter --device nvidia.com/gpu=all -e PUID=0 -p 127.0.0.1:8765:8765 -v ./data:/data:Z patter-server
```

Rootless Podman already maps root in the container to you, so `PUID=0` keeps the files yours.

`glossary.txt` is one name per line. That spelling is the spelling that gets typed. It is read on every request, so edits need no restart.

`/health` takes no token. `/v1/transcribe`, `/v1/sanitize`, and `/v1/dictate` want `Authorization: Bearer` and the token file.

## Desktop

Build needs Go, a C compiler, and the GTK 3, Ayatana AppIndicator, X11, and XTest headers. On Debian or Ubuntu:

```
sudo apt install golang gcc libgtk-3-dev libayatana-appindicator3-dev libx11-dev libxtst-dev
```

At run time it calls `ffmpeg`, `pactl`, and `xdotool`. `notify-send` shows errors and `copyq` is used for pasting when present.

Copy the server's `token` file to this machine, then:

```
PATTER_SERVER=http://gpu-box:8765 PATTER_TOKEN_FILE=./token ./install.sh
```

It builds `~/.local/bin/patter`, writes `~/.config/patter/config.json` and the token, adds a menu entry and an autostart entry, and starts the daemon. Rerun it to update. The server address and everything else can be changed in the window afterwards.

`fallback` in the config is a second URL. It is used only when the first address does not connect.

## The window

On Linux the daemon puts an icon in the system tray and opens a window. The icon is a check while the microphone is flowing and the speech server answers `/health`. It changes to a microphone while you hold the key. Closing or minimizing the window leaves the icon in the tray. The tray menu can show the window again, and Quit is in that menu. Quitting releases the key. Starting the daemon again while it is already running shows the window.

The left side is status and the hotkey. Click the key to press a new one. The right side is one setting per row: Microphone, Server, Second, Speech, Cleanup, Language. Microphone lists the inputs the sound server has. An input that is unplugged falls back to the system default until it is back. Speech and Cleanup are picked from a list, or a Hugging Face id or local path is typed into the same field.

The buttons are Start and Stop, which arm and disarm the hotkey without quitting, then Help, Restart, and Save. Help swaps the settings for a short page on talking, corrections, names, and models. Restart starts the daemon again with the saved settings. Save writes the settings and restarts. Both wait for the key to be up and the last line to land.

A model that is not on the server yet is downloaded when you Save. The server keeps answering with the old model while the files come down, then swaps under the GPU lock. The window shows Downloading with a bar, then Loading, then Ready. A key pressed after the words are on screen leaves that text alone, so Enter does not type the ending a second time.

Holding the key and typing works on Linux with X11. macOS and Windows do not have the key grab yet.

## Limits

- Linux with X11 only. The key grab, the typing, and the tray are X11 and GTK 3. Wayland, macOS, and Windows are not done.
- Plain HTTP with a bearer token. Keep the server on a LAN or a VPN you trust. Audio is sent only while the key is held.
- A very short hold can produce invented words while the first partial is on screen. The cleanup pass on release replaces it.
- The speech model needs about 7 GB of VRAM with the cleanup model loaded. A 12 GB card is comfortable.

## Left on your machine

`config.json`, `glossary.txt`, `token`, and `data/` are local. `.gitignore` keeps them out of the repo.
