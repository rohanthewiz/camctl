# Session: Mac Installer + Low-Hanging-Fruit Analysis

Session ID: `15fa0dcc-0876-4a7e-99bc-95a315ea41fe`
Date: 2026-07-18
Branch: `master`

## What was done

### 1. Merged `roh/sound-rec-ndi-chgs` into `master`
Clean fast-forward bringing in commit `c76a979` — removed the CGo NDI receiver
(`ndi/receiver.go`) in favor of the pure-Go `gondi` approach, added
`cmd/obstest/main.go` and a session doc. Verified with `go build ./...`.

### 2. Native Mac installer (`scripts/mac-install.sh`)
Ported from the kro project's `mac-install.sh` (cloned kro to scratchpad for
reference). Architecture:

- Syncs source to `~/.camctl/src` (owns that dir — `git reset --hard` to
  `origin/master` on each run; user data in `~/.camctl/camctl.db` untouched).
- Resolves Go >= 1.25.5: system Go first, else auto-installs a private copy
  under `~/.local/go`.
- Builds with `-trimpath -tags ndi -ldflags "-s -w"`. The `ndi` tag matters:
  the plain `scripts/install.sh` builds the stub previewer (no `-tags ndi`) —
  still worth fixing there.
- Compiles a Swift/WebKit wrapper into `~/Applications/CamCtl.app`:
  starts the bundled server (reuses one already running), waits for readiness,
  shows the UI in a native window. Includes minimal app/Edit menus (Cmd+Q/C/V),
  the Cmd+Tab icon-cache workaround, and PATH prepends so Homebrew ffmpeg is
  found for the RTSP fallback. Dropped kro's WKDownload section (no export
  buttons in camctl).
- Generated app icon: white camera-lens motif on blue→violet gradient squircle,
  rendered by a throwaway AppKit program; non-fatal if headless.
- Server logs: `~/Library/Logs/CamCtl/camctl.log`.
- Env overrides: `CAMCTL_REPO_URL`, `CAMCTL_SRC_DIR`, `CAMCTL_GO_VERSION`,
  `CAMCTL_GO_DIR`, `CAMCTL_APP_DIR`, `CAMCTL_APP_NAME`, `CAMCTL_PORT`.

Install one-liner (live on master):
```
curl -fsSL https://raw.githubusercontent.com/rohanthewiz/camctl/master/scripts/mac-install.sh | bash
```

### 3. Supporting server changes
- `handlers.go`: `GET /health` readiness route (JSON `{"status":"ok"}`).
  The Swift wrapper treats *any* HTTP response as ready, so it also works
  against older builds without the route.
- `main.go`: `CAMCTL_PORT` env override (default 8383).
- Verified live: ran server on port 8399, curled `/health` (ok) and `/` (200).

### 4. Ran the installer end-to-end
Installed `~/Applications/CamCtl.app` successfully (built from remote master
`7f6e9de` at the time; re-run installer after any push to update the app).

### 5. Committed and pushed
`7fde6d2` on master: installer + `/health` + `CAMCTL_PORT`. The push also
carried the earlier gondi merge `c76a979`.

## Analysis findings (not yet implemented)

### Testing (zero test files exist)
1. `visca` — buildFrame framing, seq increment, command payload bytes; inject a
   `net.Pipe` conn in-package. Highest value (talks to physical hardware).
2. `storage` — DuckDB in temp dir: CRUD, `UpdateCamera` rename transaction,
   seed idempotence, JSON migration.
3. `ndi.readFrames` MJPEG splitter — **latent bug**: SOI/EOI markers split
   across read-chunk boundaries are missed (`bytes.Index` per chunk), merging
   frames until the next intact marker. A table test would catch it.
4. `obsAuthString` — known-answer test from the OBS WebSocket spec.
5. No CI — GitHub Actions: vet + build both tag variants + `go test -race`.
   Known issues it would flag: data race — `handleCameraRemove`/`handleCameraEdit`
   read `a.Settings` without `a.mu` (handlers.go:357, 395–399); staticcheck
   S1009 at handlers.go:484 (`frame == nil || len(frame) == 0`).

### Performance (all preview path)
1. `Frame()` copies the whole JPEG per client per 100ms — producers always
   store freshly allocated buffers, so it can return the slice directly.
2. `handlePreview` resends unchanged frames every 100ms — add a generation
   counter, send only new frames.
3. `captureNDI` JPEG-encodes every incoming frame (30–60fps) but consumers
   read at 10fps — throttle encoding (~3–6x CPU cut on the hottest path).

### Usability
1. **No README** — biggest win for a public repo with a curl|bash installer.
2. **LAN exposure** — binds all interfaces, no auth; settings page echoes the
   stored OBS WebSocket password in plaintext HTML (views.go:227). Suggest:
   default bind `127.0.0.1` (env override), never echo the saved password.
3. Invalid port input silently falls back to 52381 in `handleSettings` /
   `handleCameraEdit` — reject instead.
4. Error JSON returned with HTTP 200 — use proper 4xx codes.
5. `handleSettings` tears down the working camera before attempting the new
   connection — connect first, swap on success.

Suggested first pass: README + localhost bind/password echo, Frame() copy +
dedup send, visca + storage tests with CI.

## Key files
- `scripts/mac-install.sh` — the Mac installer (new)
- `main.go` — CAMCTL_PORT override
- `handlers/handlers.go` — /health route
- `scripts/install.sh` — plain CLI installer (still missing `-tags ndi`)
