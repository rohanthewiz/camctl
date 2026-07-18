# CamCtl

A lightweight web-based controller for PTZ cameras that speak **VISCA over IP**
(most NDI PTZ cameras — PTZOptics, BirdDog, AVer, OBSBOT, and similar).
Point your browser at a local page and get pan/tilt/zoom controls, position
presets, and a live video preview — no vendor app required.

Written in Go with a minimal dependency footprint. Camera settings and presets
persist in an embedded DuckDB database under `~/.camctl/`.

## Features

- **Pan / tilt / zoom** control with press-and-hold movement
- **Presets** — save, recall, and label up to camera-supported preset slots
- **Multiple cameras** — save several cameras and switch between them
- **Live preview** in the browser (MJPEG), sourced from the first strategy
  that works, in order:
  1. **NDI direct** from the camera (requires the NDI runtime / `libndi`)
  2. **OBS WebSocket** screenshots (OBS Studio with the WebSocket server enabled)
  3. **HTTP snapshots** from common camera JPEG endpoints
  4. **RTSP** via `ffmpeg`
- **Health endpoint** at `/health` for wrappers and monitoring

## Install

### macOS app (recommended on a Mac)

Installs a native `CamCtl.app` into `~/Applications` — a WebKit window that
starts the bundled server and shows the UI:

```sh
curl -fsSL https://raw.githubusercontent.com/rohanthewiz/camctl/master/scripts/mac-install.sh | bash
```

The installer syncs source to `~/.camctl/src`, uses your system Go (or installs
a private copy under `~/.local/go`), and builds with NDI preview enabled.
Server logs go to `~/Library/Logs/CamCtl/camctl.log`. Re-run the installer any
time to update.

### CLI install (macOS / Linux)

```sh
curl -fsSL https://raw.githubusercontent.com/rohanthewiz/camctl/master/scripts/install.sh | bash
```

### From source

```sh
git clone https://github.com/rohanthewiz/camctl.git
cd camctl
go build -tags ndi -o camctl .   # omit "-tags ndi" to build without NDI preview
./camctl
```

The `ndi` build tag compiles the preview subsystem (NDI is loaded dynamically
via purego — no CGo, and the other strategies need no SDK). Without the tag a
stub previewer is used and in-app preview is disabled entirely, so build with
`-tags ndi` unless you specifically want a control-only binary.

Then open <http://localhost:8383>.

## Usage

1. Open the settings panel (gear icon) and enter your camera's IP.
   The VISCA port defaults to `52381`; some cameras use `1259` or `5678` —
   check your camera's network/VISCA settings page.
2. Connect. The app verifies the camera responds before reporting success.
3. Use the arrow pad to pan/tilt, the zoom buttons to zoom, and the numbered
   slots to save/recall presets. Presets can be labeled.
4. Optional: open preview settings to choose preview sources and configure
   the OBS WebSocket host/password if you use the OBS strategy.

## Configuration

All configuration is via environment variables; state lives in `~/.camctl/`.

| Variable | Default | Purpose |
|---|---|---|
| `CAMCTL_PORT` | `8383` | Web UI port |
| `CAMCTL_BIND` | `127.0.0.1` | Bind address. The app has **no authentication**, so it binds to localhost only. Set `CAMCTL_BIND=0.0.0.0` to deliberately expose it to your LAN (e.g. to control cameras from a tablet) — do this only on a trusted network. |
| `OBS_WS_HOST` | — | OBS WebSocket host override for the preview (also settable in the UI) |
| `OBS_WS_PASSWORD` | — | OBS WebSocket password (also settable in the UI) |

Installer-specific overrides (`CAMCTL_REPO_URL`, `CAMCTL_SRC_DIR`,
`CAMCTL_GO_VERSION`, `CAMCTL_GO_DIR`, `CAMCTL_APP_DIR`, `CAMCTL_APP_NAME`) are
documented at the top of `scripts/mac-install.sh`.

## Development

```sh
go build ./...        # stub previewer (no NDI runtime needed)
go build -tags ndi ./...
go test -race ./...
```

Project layout:

- `main.go` — entry point, server setup
- `handlers/` — HTTP routes and app state
- `views/` — HTML generation ([element](https://github.com/rohanthewiz/element)), JS, CSS
- `visca/` — VISCA-over-IP client (UDP framing + camera commands)
- `ndi/` — preview strategies (NDI via purego, OBS WebSocket, HTTP, RTSP)
- `storage/` — DuckDB persistence (cameras, presets, preview settings)
- `scripts/` — install scripts
