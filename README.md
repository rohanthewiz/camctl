# CamCtl

A lightweight web-based controller for PTZ cameras that speak **VISCA over IP**
(most NDI PTZ cameras — PTZOptics, BirdDog, AVer, OBSBOT, and similar).
Point your browser at a local page and get pan/tilt/zoom controls, position
presets, and a live video preview — no vendor app required.

Written in Go with a minimal dependency footprint. Camera settings and presets
persist in an embedded [bytdb](https://github.com/rohanthewiz/bytdb) database at
`~/.camctl/camctl.bytdb`. bytdb is pure Go, so camctl builds with
`CGO_ENABLED=0` and needs no C toolchain.

## Features

- **Pan / tilt / zoom** control with press-and-hold movement
- **Presets** — save, recall, and label up to camera-supported preset slots,
  scoped per camera
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

Requires Go 1.26.1+ (the floor set by the bytdb storage engine).

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

### Upgrading from a DuckDB-era install

camctl used to keep its data in DuckDB at `~/.camctl/camctl.db`. Storage now
uses bytdb at `~/.camctl/camctl.bytdb`. The two file formats are unrelated, so
the old database is converted rather than opened:

```sh
go run ./cmd/dbmigrate                            # uses the defaults below
go run ./cmd/dbmigrate -from old.db -to new.bytdb # explicit paths
go run ./cmd/dbmigrate -force                     # replace an existing target
```

| Flag | Default | Purpose |
|---|---|---|
| `-from` | `~/.camctl/camctl.db` | Legacy DuckDB database to read |
| `-to` | `~/.camctl/camctl.bytdb` | bytdb database to create |
| `-force` | off | Overwrite the destination if it already exists |

Both installers run this automatically when they find an old database and no new
one, so most people never need to. The old file is only read — it is left in
place, and you can delete it once camctl looks right.

Without `-force` the converter refuses to overwrite an existing `camctl.bytdb`,
which is what makes a second run safe: re-importing would merge the old rows
back over anything you have changed since, resurrecting deleted cameras and
stale preset labels.

`cmd/dbmigrate` is the only part of the project that uses DuckDB (and therefore
CGo); keeping it out of the app is what lets camctl ship as a pure-Go binary.

## Usage

1. Open the settings panel (gear icon) and enter your camera's IP.
   The VISCA port defaults to `52381`; some cameras use `1259` or `5678` —
   check your camera's network/VISCA settings page.
2. Connect. The app verifies the camera responds before reporting success.
3. Use the arrow pad to pan/tilt, the zoom buttons to zoom, and the numbered
   slots to save/recall presets. Presets can be labeled.
   Presets belong to a single camera — the positions live in the camera itself,
   and the labels are stored per camera — so each camera you add starts with its
   own empty set. The name beside the "Presets" heading shows which camera the
   slots on screen belong to, and the grid swaps when you switch cameras.
4. Optional: open preview settings to choose preview sources and configure
   the OBS WebSocket host/password if you use the OBS strategy.

## Configuration

All configuration is via environment variables; state lives in `~/.camctl/`
(`camctl.bytdb` holds cameras, preset labels, and preview settings — the only
file worth backing up).

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
go build ./...                                  # stub previewer (no NDI runtime needed)
go build -tags ndi ./...
CGO_ENABLED=0 go build -tags ndi -o camctl .    # how the installers build it
go test -race ./...
```

The app is pure Go and CI builds it with `CGO_ENABLED=0` to keep it that way —
if a change adds a CGo dependency to camctl itself, that step is what fails.
Note that `./...` is wider than the app: it also covers `cmd/dbmigrate`, whose
DuckDB driver does use CGo, so `go build ./...` and `go test -race ./...` still
want a C toolchain (`-race` requires one regardless).

Project layout:

- `main.go` — entry point, server setup
- `handlers/` — HTTP routes and app state
- `views/` — HTML generation ([element](https://github.com/rohanthewiz/element)), JS, CSS
- `visca/` — VISCA-over-IP client (UDP framing + camera commands)
- `ndi/` — preview strategies (NDI via purego, OBS WebSocket, HTTP, RTSP)
- `storage/` — bytdb persistence (cameras, presets, preview settings)
- `cmd/dbmigrate/` — one-time converter for pre-bytdb (DuckDB) databases
- `scripts/` — install scripts
