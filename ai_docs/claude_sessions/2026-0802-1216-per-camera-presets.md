# Session: Per-Camera Presets (+ rweb form-value aliasing bug)

Session ID: `f79b23d7-e262-4c0d-8dd2-ba9e5baa8582`
Date: 2026-08-02
Branch: `master`
Previous session: `2026-0718-1308-first-and-second-pass-fixes.md`

## Reported symptom

> "When I add a second camera, I don't get a new set of presets"

## Root cause

`storage/storage.go` defined presets keyed by slot number alone:

```sql
CREATE TABLE presets (number INTEGER PRIMARY KEY, label TEXT NOT NULL);
```

One global set of six labels shared by every camera, so camera #2 rendered
camera #1's preset names.

Important nuance: the **positions** were never shared — VISCA `PresetSet`/
`PresetRecall` address slots in the camera's own memory, so each camera always
had its own physical six. Only camctl's *labels* were global.

## Changes

### storage/storage.go

- New schema: `presets (camera_label TEXT, number INTEGER, label TEXT,
  PRIMARY KEY (camera_label, number))`.
- **No FOREIGN KEY** to `cameras` — DuckDB can't do `ON DELETE CASCADE`, and an
  un-cascaded FK would block camera deletion. Cascade is done by hand instead.
- `migratePresetsSchema()` — detects the old shape via
  `information_schema.columns`, renames `presets` → `presets_legacy`, then
  creates the new table. Rename (not `ALTER TABLE`) keeps the table definition
  in one place so all other queries can assume the new schema.
- `fanOutLegacyPresets()` — copies parked legacy labels onto **every** saved
  camera (`INSERT OR IGNORE ... SELECT`), then drops the holding table. With no
  cameras saved, the labels were unreachable anyway, so it just drops.
- `pruneOrphanPresets()` — startup sweep deleting preset rows whose camera no
  longer exists (presets seed lazily on read, so a never-saved camera name can
  leave rows behind).
- `ensurePresets(cameraLabel)` — lazy per-slot `INSERT OR IGNORE` top-up. Called
  on read and on camera insert. Per-slot rather than count-based because
  migrated rows can be sparse (e.g. only slots 0 and 3).
- `DefaultPresets()` / `defaultPresetLabel(num)` — placeholder set for "no
  camera selected".
- API changes: `AllPresets(cameraLabel)`, `UpdatePresetLabel(cameraLabel, num,
  label)`. Empty `cameraLabel` → `AllPresets` returns placeholders (so the UI can
  render), `UpdatePresetLabel` errors (nowhere to store it).
- `UpsertCamera` now also provisions the camera's slots. Split into
  `upsertCameraRow` + `ensurePresets` so the JSON migration path can write the
  camera row *without* seeding — see the ordering bug below.
- `UpdateCamera` rename re-keys preset rows inside the existing transaction
  (clearing any stale rows under the new label first, to avoid a PK collision).
- `RemoveCamera` deletes the camera and its presets in one transaction.

### storage/migrate.go

`migratePresetsJSON` now writes into `presets_legacy` (creating it if needed) so
the old global `presets.json` takes the same fan-out path as an old DuckDB table.

`migrateCamerasJSON` switched to `upsertCameraRow`.

**Ordering bug hit during this work:** with `UpsertCamera` seeding placeholders,
JSON camera migration filled slots 0–5 *before* `fanOutLegacyPresets` ran, so its
`INSERT OR IGNORE` skipped every real label. Caught by `TestJSONMigration`. Fix
was the `upsertCameraRow` split — provisioning must wait until after fan-out.

### handlers/handlers.go

- `presetsFor(cameraLabel)` — loads presets, falls back to placeholders on a
  storage error (degrades to unnamed-but-usable, not empty).
- `presetsJSON(cameraLabel)` + new `Presets []presetJSON` field on
  `settingsResponse`, populated in all four response sites (connect success,
  connect failure, camera remove, camera edit) so the client can repaint without
  a reload.
- `handlePresetLabel` takes the camera from **server-side** `a.Settings.CameraLabel`
  under `RLock`, not a client field — the label being edited is by definition the
  one on screen. Errors with "select a camera before naming presets" when unset.
- `handleIndex` renders the active camera's presets.

### views/views.go

- `PresetsView` gains `CameraLabel`; renders a `.presets-header` row with a
  `#presets-camera` span naming the owning camera (or "select a camera").
- Card markup extracted to `RenderPresetCards(b, presets)`; grid wrapped in
  `#preset-grid` so JS can target it.

### views/app.js

- `updatePresets(presets, cameraLabel)` — repaints heading + grid; mirrors
  `RenderPresetCards`.
- `escapeAttr(s)` — preset labels are free-form operator input interpolated into
  `value="..."`. `textContent`/`innerHTML` escapes `& < >` only, so quotes are
  replaced explicitly.
- Wired into `doConnect` (both outcomes — the server switches active camera even
  on a failed connect), `saveEditCamera`, and `removeCamera`.

### views/styles.css

`.presets-header` flex row, `.presets-camera` (accent) and `.presets-camera.none`
(dim italic). The h2's bottom margin moved onto the flex row.

### README.md

Notes that presets are per-camera and that the heading shows which camera the
grid belongs to.

## Second bug found: rweb form values alias a reused buffer

Live testing showed the status bar rendering:

```
Connected — 0alcony (Wide Balc:52382)
```

`Balcony` → `0alcony`, and the stored IP `127.0.0.1` (9 bytes) → `Wide Balc`
(first 9 bytes of a *later* request's form value).

**Cause:** rweb's `FormValue` → `GetPostValue` → `b2s(req.PostArgs().Peek(key))`
— an unsafe zero-copy `[]byte`→`string` view over `req.body`, which is reused for
the next request on the connection. Anything retained past the handler silently
mutates.

**Pre-existing**, not introduced here — the saved camera IP in `App.Settings` was
already being corrupted (affects preview restart, which uses
`a.Settings.CameraIP`). It became load-bearing for this feature because presets
are keyed by `CameraLabel`, so a corrupted label would read/write the wrong
camera's presets.

**Fix:** `formValue(c, key)` helper wrapping `strings.Clone`; every form read in
`handlers.go` routed through it rather than leaving the safe/unsafe distinction
to be re-derived per call site.

## Tests (storage/storage_test.go)

- `TestPresetsAreScopedPerCamera` — regression guard for the reported bug: second
  camera starts from placeholders; same slot number on two cameras coexists;
  rename carries presets across; delete + re-add does not resurrect labels.
- `TestLegacyGlobalPresetsMigrate` — builds a pre-migration DB by hand via raw
  `sql.Open("duckdb", ...)`, verifies both cameras inherit the old labels topped
  up to a full set, that editing one no longer touches the other, and that the
  holding table is gone.
- `TestJSONMigration` extended: legacy `presets.json` labels land on the migrated
  camera, gaps filled with placeholders.
- `TestUpdatePresetLabel` — empty camera label must error.
- `mustPresets(t, d, label)` helper.

All tests pass; `go vet` clean.

## Verification

Migration checked against a **copy** of the real `~/.camctl` DB and the repo-root
`camctl.db`: existing camera `HouseCamm` kept its `Pulpit` label; a newly added
camera got a clean set.

Live end-to-end run (app on `:8391` with `HOME` pointed at a scratch dir, two
fake VISCA cameras echoing UDP on 52381/52382):

```
### HouseCamm (migrated legacy labels)
  heading: HouseCamm
  slots:   0:Pulpit | 1:Preset 2 | ... | 5:Preset 6
### Balcony (second camera)
  heading: Balcony
  slots:   0:Wide Balcony | 1:Preset 2 | ... | 5:Preset 6
### switch to HouseCamm — must be unaffected
  heading: HouseCamm
  slots:   0:Pulpit | ...
### delete Balcony then re-add it — labels must not resurrect
  heading: Balcony
  slots:   0:Preset 1 | ...
```

Status bar after the clone fix: `Connected — Balcony (127.0.0.1:52382)`.

Browser extension was not connected, so the UI was verified by parsing rendered
HTML rather than by screenshot. `node --check views/app.js` for the JS.

## Notes / gotchas discovered

- The `element` builder emits attributes in **non-deterministic order** (map
  iteration). An early verification grep assumed `id` preceded `value` and gave
  false negatives. Don't write order-dependent assertions against its output.
- `views/app.js` embeds a JS template containing `preset-card` / `preset-label-`
  literals, so regexes over the rendered page must be scoped to `#preset-grid`.

## Scratch tooling archived

`arch_test_scripts/dbcheck.go.txt` — opens a camctl DB through `storage.Open`,
optionally seeds `label:ip` cameras, prints cameras + their presets.
`arch_test_scripts/fakecam.go.txt` — UDP echo server standing in for a VISCA
camera (any reply satisfies `Connect()`'s liveness probe).

## Behavior change worth knowing

On a fresh app start nothing is connected, so the grid shows placeholders and
"select a camera" until a camera is clicked. Previously it always showed the one
global label set. Label editing while disconnected is rejected with a toast.

## Rebase onto concurrent work

`origin/master` had moved ahead by three commits (`80abe9d`, `b92f502`,
`4732656` — Mac app preset save feedback + WKUIDelegate), touching `views/app.js`
and `views/styles.css`. Rebased; git auto-merged, but two semantic overlaps
needed hand-fixing:

- The incoming commit factored the "is this slot configured?" highlight rule into
  `isConfiguredLabel(num, label)` in app.js. My `updatePresets` had its own
  inline copy of that rule — switched to call `isConfiguredLabel` so the repaint
  and `saveLabel` can't drift.
- That new JS function's comment says it mirrors the server rule in views.go, but
  views.go only compared against `"Preset N"` and did not treat an empty label as
  unconfigured. Added `isConfiguredLabel(num, label)` to views.go (trimmed,
  non-empty, not the placeholder) and used it in `RenderPresetCards`, so the two
  sides actually agree — otherwise a slot would change appearance between a live
  repaint and the next page load.

Re-ran the full live verification after the rebase; all per-camera behavior and
the highlight classes still correct.

## Commits (all pushed to `origin/master`)

| Commit | Summary |
| --- | --- |
| `c30920d` | Scope presets per camera; fix rweb form-value buffer aliasing |
| `98dd378` | Share one configured-label rule between views.go and app.js (+ this doc) |
| `0e31db7` | Track `.cats-todo` task list |

`.cats-todo/todos.json` is now tracked at the user's request. It holds one open
item: *move the storage backend to `github.com/rohanthewiz/bytdb`, updating the
Mac installer with it.*

## Outstanding

- **`bytdb` migration** (`.cats-todo`) — note that this session reshaped the
  DuckDB schema substantially: composite-key `presets (camera_label, number)`,
  the `presets_legacy` fan-out migration, `information_schema` probing for the
  schema upgrade, and hand-rolled cascades on camera rename/delete (DuckDB has no
  `ON DELETE CASCADE`). All of that needs porting, and whether bytdb offers
  transactions matters for `UpdateCamera`/`RemoveCamera`, which rely on them to
  keep camera and preset rows consistent.
- No LICENSE file (carried over from the previous session).
- The rweb aliasing issue is worked around app-side; upstream `rweb` still
  returns buffer-aliased strings from `FormValue`.
