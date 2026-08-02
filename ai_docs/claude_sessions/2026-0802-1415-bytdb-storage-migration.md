# Session: Storage backend moved from DuckDB to bytdb

Session ID: `43324321-e79d-4ff2-88e2-91b6394c98f0`
Date: 2026-08-02
Branch: `master`
Previous session: `2026-0802-1216-per-camera-presets.md`

## Task

The open `.cats-todo` item: *move the storage backend to
`github.com/rohanthewiz/bytdb`, updating the Mac installer with it.*

## Why it was worth doing

The DuckDB driver uses CGo and carries ~60 MB of prebuilt static libraries.
That single dependency set the shape of the whole build: the macOS installer
required Xcode Command Line Tools partly for `clang`, and the shipped binary was
60 MB. bytdb is pure Go, so the app now builds with `CGO_ENABLED=0`.

**Binary: 60 MB -> 9.3 MB**, with zero DuckDB in the app's dependency graph
(`go list -deps .` — 0 duckdb packages; `cmd/dbmigrate` — 5).

## Reconnaissance before writing code

bytdb ships a `database/sql` driver (`bytdb/stdlib`, registered as `"bytdb"`),
so the port kept the same shape as the DuckDB code rather than becoming a
rewrite against a KV API. What it does *not* share is the dialect. A throwaway
probe program exercised every statement the storage package issues, rather than
inferring support from the parser source:

| Feature | bytdb |
| --- | --- |
| `$1` parameters | yes |
| `?` parameters | **no** — syntax error |
| `INSERT OR IGNORE` / `INSERT OR REPLACE` | **no** — syntax error |
| `ON CONFLICT (cols) DO NOTHING` / `DO UPDATE ... excluded.col` | yes |
| `CREATE TABLE IF NOT EXISTS` | **no** — syntax error |
| `DROP TABLE IF EXISTS` | **no**; plain `DROP` of an absent table errors |
| Multiple statements in one `Exec` | **no** — syntax error |
| `INSERT ... SELECT` | **no** — syntax error |
| Composite `PRIMARY KEY (a, b)` | yes |
| `BOOLEAN` + `DEFAULT` | yes |
| `WHERE x NOT IN (SELECT ...)` | yes |
| `information_schema.tables` / `.columns` | yes |
| `ALTER TABLE ... RENAME TO` | yes |
| `RowsAffected` | yes |
| Transactions (Begin/Commit/Rollback, `ErrTxDone`) | yes |
| DDL inside a transaction | refused, by design |
| Reopen the same path after `Close` | yes |

The probe is the reason the port landed correctly the first time. Reading
`sql/parser.go` alone would have been misleading in both directions — it
suggested `IN (subquery)` might be missing (it is supported) and gave no signal
that `INSERT ... SELECT` is absent.

## Changes

### storage/storage.go

Public API unchanged, so `handlers/` needed no edits at all. The rewrite is
mechanical except where the dialect forced a structural change:

- `$1` placeholders throughout.
- `insertPresetIfAbsent` — one helper for what used to be `INSERT OR IGNORE`,
  now `ON CONFLICT (camera_label, number) DO NOTHING`. Used by both
  `ensurePresets` and `fanOutLegacyPresets`.
- `UpdatePresetLabel` / `upsertCameraRow` — `ON CONFLICT ... DO UPDATE SET
  col = excluded.col`.
- `ensureTable(name, ddl)` — stands in for `CREATE TABLE IF NOT EXISTS` by
  probing `information_schema.tables` first. Checking beforehand (rather than
  creating and swallowing "table already exists") keeps a genuine DDL failure
  from being misread as an existing table.
- `dropTableIfExists` — same idea for `DROP TABLE IF EXISTS`.
- `createTables` — one `Exec` per statement; the old semicolon-separated block
  is a syntax error in bytdb.
- `fanOutLegacyPresets` — reads the parked rows into memory via
  `legacyPresets()` and writes them back per camera, since there is no
  `INSERT ... SELECT`. Safe at this size (one global set, six slots) and it
  avoids writing to a table while a cursor iterates it.
- `Open` now calls `Ping()`. `sql.Open` is lazy, so without it a bad file
  surfaces from an arbitrary later query instead of at open time.

**The foreign key is still absent, but for a different reason than before.**
The DuckDB comment said "DuckDB does not support ON DELETE CASCADE". bytdb
*does*. The real blocker is that the parent key would have to be
`cameras.label` — the very column a rename changes — and bytdb, like Postgres,
has no `ON UPDATE CASCADE`. An FK would refuse the rename while preset rows
referenced the old label. The hand-rolled cascade in `UpdateCamera` /
`RemoveCamera` stays, and the comment now says why.

Also noted in the comments: the composite PK `(camera_label, number)` doubles as
the read path, making "all presets for one camera, in slot order" a bounded
ordered prefix scan rather than a full scan.

### The data-loss trap (most important finding)

Handing bytdb the existing DuckDB `camctl.db` **silently succeeds**. bytdb reads
the foreign bytes as a torn write-ahead-log tail, repairs them by truncating,
and reports a perfectly healthy *empty* database — then overwrites the real
contents on the first commit. To the operator that reads as "all my cameras
vanished", followed by the file that still held them being destroyed.

Two defenses:

1. The new database is `camctl.bytdb`, a different filename, so the old file is
   never passed to bytdb in the first place.
2. `rejectLegacyDuckDBFile(dbPath)` runs before `sql.Open` and refuses a file
   carrying DuckDB's magic (an 8-byte checksum then the literal `DUCK` at
   offset 8), naming `cmd/dbmigrate` in the error.

Covered by `TestRejectsLegacyDuckDBFile`, which also asserts the legacy file is
left byte-for-byte intact.

### cmd/dbmigrate (new)

One-shot converter, and the only place DuckDB is imported. Keeping it out of the
app is what buys the pure-Go binary.

- Handles **both** historical preset shapes: per-camera `(camera_label, number)`,
  and the older global set keyed by number alone, which it fans out to every
  camera — matching what `storage.Open` does when upgrading in place.
- Refuses to overwrite an existing `camctl.bytdb` without `-force`. A second run
  would otherwise merge old rows back over later edits, resurrecting deleted
  cameras and stale labels.
- `-force` clears the sidecar `.wal` files too, so it starts from genuinely
  empty state instead of replaying an old log over the new one.
- Writes through the `storage` API (`UpsertCamera`, `UpdatePresetLabel`) rather
  than raw SQL, so seeding, keying, and validation stay in one place. An
  out-of-range slot from a hand-edited database is logged and skipped, not fatal.

**`?access_mode=read_only` on the source is load-bearing, not a precaution.**
The first version of the tool opened DuckDB read-write, and `cmp` showed the
source file *was* modified — a read-write DuckDB handle checkpoints its WAL into
the main database file on close. That contradicted the tool's central promise.
Read-only still replays the WAL in memory, so nothing is missed. Now verified
byte-identical after a migration.

Also fixed: `run()` takes a **named** error return so the deferred `dst.Close()`
can surface a late write failure. bytdb reports deferred background failures
(WAL sync, compaction) from `Close`, and the original closure compared against
an `err` that the `return` statements never assigned — so it could `log.Fatal`
on a path that should have deferred to the caller.

### storage/migrate.go

`CREATE TABLE IF NOT EXISTS` -> `ensureTable`; `INSERT OR REPLACE` ->
`ON CONFLICT (number) DO UPDATE SET label = excluded.label`.

### Installers

- **`scripts/mac-install.sh`** — new `migrate_legacy_db()` runs the conversion
  when it finds `camctl.db` and no `camctl.bytdb`, non-fatally: the app is
  already built and working at that point, and a conversion failure should not
  undo that. App build now uses `CGO_ENABLED=0`, both as a size win and as a
  guard against silently re-adding a compiler requirement. The `require_swiftc`
  comment was corrected — swiftc is still needed for the Swift/WebKit wrapper,
  but clang is now only used by the on-demand DuckDB conversion.
- **`scripts/install.sh`** — same conversion block, Go floor 1.25 -> 1.26,
  `CGO_ENABLED=0` build.
- Go version floor is bytdb's requirement (`go 1.26.1`); `go mod tidy` bumped
  camctl's own directive and pulled serr 1.2.21 -> 1.4.0 along with it.

### CI

Added a `CGO_ENABLED=0 go build -tags ndi -o /dev/null .` step. It guards the
exact property the installers depend on — a stray CGo dependency in the app
would otherwise only show up as a failed install on a user's machine.

### Docs

README: bytdb + `camctl.bytdb` in the intro, a new "Upgrading from a DuckDB-era
install" section, Go 1.26.1+ prerequisite, `cmd/dbmigrate/` in the project
layout, and a Development note that `./...` is wider than the app — it covers
`cmd/dbmigrate`, so `go build ./...` and `-race` still want a C toolchain even
though the app itself does not. `.gitignore` gained `*.bytdb` / `*.bytdb.wal`.

## Tests

- `TestRejectsLegacyDuckDBFile` (new) — the guard above.
- `TestOrphanPresetsPrunedOnReopen` (new) — `Open`'s orphan sweep, and
  specifically the SQL semantics it leans on: the delete filters with `NOT IN`
  over a subquery, and with no cameras saved that subquery is **empty**, which
  must match every row rather than none. Also asserts the sweep spares a
  genuinely saved camera. Neither half was covered before.
- `TestLegacyGlobalPresetsMigrate` — the hand-built pre-migration database now
  seeds through the `bytdb` driver, statement by statement.
- Test DB filenames `test.db` -> `test.bytdb`; the `openTestDB` comment now
  explains the real reason each test needs its own directory (bytdb takes a
  database file for itself: one path is one engine per process).

`go vet ./...` clean; `go test -race ./...` green.

## Verification against real data

Migrated the repo's actual `camctl.db` (camera `HouseCamm`, slot 0 labeled
`Pulpit`, in the *old global* preset shape) and read it back through
`arch_test_scripts/dbcheck.go.txt` — camera and label intact, remaining slots
filled with placeholders. Source file confirmed byte-identical afterwards.

`~/.camctl/camctl.db` turned out to hold zero cameras and only placeholder
labels, so the meaningful test data was the repo-root copy.

Live end-to-end run of the built app against the migrated database (fake VISCA
cameras echoing UDP on 52381/52382, `HOME` pointed at a scratch dir):

```
connect HouseCamm      -> presets: 0:Pulpit | 1:Preset 2 | ...
relabel slot 2         -> Choir Loft
switch to Balcony      -> 0:Preset 1 | ...   (no leakage)
rename Balcony         -> Balcony Left, presets follow
back to HouseCamm      -> 0:Pulpit | 2:Choir Loft   (intact)
Balcony Left           -> 0:Wide Balcony            (its own)
preview settings POST  -> persisted verbatim
remove Balcony Left    -> gone, labels gone with it
restart the app        -> everything above survived
```

Installer logic was exercised directly by extracting `migrate_legacy_db` from
`mac-install.sh` and running it against a fake data dir: converts on first run,
silent no-op on the second, old file kept. The failure path was tested with a
deliberately corrupt legacy file — it warns and the installer continues under
`set -e` (`INSTALLER CONTINUED (exit=0)`).

## Gotchas worth remembering

- **Probe, don't infer.** bytdb's parser source suggested the wrong answer in
  both directions. A 100-line throwaway program settled every question.
- **A read-write DuckDB open mutates the file on close** (WAL checkpoint). Any
  tool claiming to read a DuckDB database non-destructively needs
  `?access_mode=read_only`.
- **bytdb accepts a foreign file and reports it as empty.** Never point it at a
  path that might hold another engine's data.
- `go mod tidy` will raise the module's `go` directive to satisfy a dependency's
  floor — it moved camctl from 1.25.5 to 1.26.1 without being asked, which then
  has to be reflected in both installers.

## Outstanding

- `.cats-todo/todos.json` still marks the bytdb item open — left for the user to
  flip after verifying.
- No LICENSE file (carried over from previous sessions).
- The rweb `FormValue` buffer-aliasing issue is still worked around app-side via
  `strings.Clone`; upstream `rweb` is unchanged.
- `~/.camctl/camctl.db` on this machine is now stale; it can be deleted once the
  app has been run for real.
