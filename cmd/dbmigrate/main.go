// Command dbmigrate converts a DuckDB-era camctl database to the bytdb format.
//
// camctl stored its cameras, preset labels, and preview settings in DuckDB
// until the storage backend moved to bytdb. The two file formats are unrelated,
// so the switch is not something the app can do while opening a database:
// bytdb reads a DuckDB file as a torn write-ahead-log tail, repairs it by
// truncating, and would report a healthy but empty database. This tool is the
// bridge — read with the old driver, write with the new one, once.
//
// It exists as a separate binary on purpose. The DuckDB driver uses CGo and
// carries ~60 MB of prebuilt static libraries; keeping its import out of the
// camctl binary is what lets camctl build as pure Go with CGO_ENABLED=0 and
// lets the macOS installer stop requiring a compiler toolchain for storage.
//
// Usage:
//
//	go run ./cmd/dbmigrate                       # ~/.camctl/camctl.db -> ~/.camctl/camctl.bytdb
//	go run ./cmd/dbmigrate -from old.db -to new.bytdb
//	go run ./cmd/dbmigrate -force                # overwrite an existing destination
//
// The source database is only ever read; nothing deletes or rewrites it.
package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"camctl/storage"

	_ "github.com/duckdb/duckdb-go/v2"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("dbmigrate: ")

	defFrom, defTo := defaultPaths()
	from := flag.String("from", defFrom, "path to the legacy DuckDB database")
	to := flag.String("to", defTo, "path to the bytdb database to create")
	force := flag.Bool("force", false, "overwrite the destination if it already exists")
	flag.Parse()

	if err := run(*from, *to, *force); err != nil {
		log.Fatal(err)
	}
}

// defaultPaths resolves the standard data directory (~/.camctl). A home
// directory that cannot be determined is not fatal here — the flags can still
// name both files explicitly — so the defaults simply fall back to the current
// directory.
func defaultPaths() (from, to string) {
	dir := "."
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, ".camctl")
	}
	return filepath.Join(dir, "camctl.db"), filepath.Join(dir, "camctl.bytdb")
}

// run performs the conversion. The return is named so the deferred Close below
// can report a late write failure that would otherwise be swallowed.
func run(from, to string, force bool) (err error) {
	if _, err := os.Stat(from); err != nil {
		return fmt.Errorf("legacy database %s not found — nothing to migrate", from)
	}

	// Refuse to write over a database that is already in use. A second run
	// would otherwise merge the old rows back over whatever the operator has
	// changed since, quietly resurrecting deleted cameras and stale labels.
	if _, err := os.Stat(to); err == nil && !force {
		return fmt.Errorf("%s already exists; pass -force to overwrite it", to)
	} else if err == nil {
		log.Printf("overwriting existing %s", to)
		if err := removeBytdbFiles(to); err != nil {
			return err
		}
	}

	cams, presets, prev, err := readLegacy(from)
	if err != nil {
		return err
	}

	dst, err := storage.Open(to)
	if err != nil {
		return err
	}
	// Close is where bytdb reports deferred write failures (WAL sync,
	// compaction), so a migration cannot be called successful until it returns.
	// Reporting that through the named return is what makes it visible: a plain
	// `defer dst.Close()` would discard it, and the run would claim success over
	// a database whose last writes never reached disk. An earlier failure still
	// wins — a close error is only news when nothing else went wrong.
	defer func() {
		if cerr := dst.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("closing %s: %w", to, cerr)
		}
	}()

	for _, cam := range cams {
		if err := dst.UpsertCamera(cam); err != nil {
			return fmt.Errorf("writing camera %q: %w", cam.Label, err)
		}
	}

	// Preset labels are written after their camera exists: storage prunes rows
	// that no saved camera claims, and UpdatePresetLabel is the API that keys
	// them correctly.
	for _, p := range presets {
		if err := dst.UpdatePresetLabel(p.camera, p.number, p.label); err != nil {
			// A slot outside the current range (from a hand-edited database, say)
			// is worth reporting but not worth aborting a migration over.
			log.Printf("skipping preset %d of %q: %v", p.number, p.camera, err)
		}
	}

	if prev != nil {
		if err := dst.UpdatePreviewSettings(*prev); err != nil {
			return fmt.Errorf("writing preview settings: %w", err)
		}
	}

	log.Printf("migrated %d camera(s) and %d preset label(s) from %s to %s",
		len(cams), len(presets), from, to)
	log.Printf("%s was not modified; remove it once camctl looks right", from)
	return nil
}

// removeBytdbFiles deletes a bytdb database and the sidecar files an engine may
// have left beside it, so -force starts from genuinely empty state rather than
// replaying an old log over the new one.
func removeBytdbFiles(path string) error {
	for _, p := range []string{path, path + ".wal", path + "-wal"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("removing %s: %w", p, err)
		}
	}
	return nil
}

// legacyPreset is one row of the old presets table, already resolved to the
// camera that owns it.
type legacyPreset struct {
	camera string
	number int
	label  string
}

// readLegacy pulls everything worth keeping out of the DuckDB database.
//
// It reads the tables directly rather than reusing the storage package, because
// storage now speaks bytdb; and it tolerates each table being absent or in
// either historical shape, since this tool has to cope with any vintage of
// camctl database an operator still has on disk.
func readLegacy(path string) ([]storage.Camera, []legacyPreset, *storage.PreviewSettings, error) {
	// read_only is load-bearing, not a precaution. A read-write DuckDB handle
	// checkpoints its write-ahead log into the main database file when it
	// closes, so merely opening the legacy database would rewrite it — and the
	// whole promise of this tool is that the old file survives untouched until
	// the operator is satisfied. Read-only still replays the WAL in memory, so
	// nothing is missed.
	db, err := sql.Open("duckdb", path+"?access_mode=read_only")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer db.Close()

	cams, err := readCameras(db)
	if err != nil {
		return nil, nil, nil, err
	}

	presets, err := readPresets(db, cams)
	if err != nil {
		return nil, nil, nil, err
	}

	prev, err := readPreviewSettings(db)
	if err != nil {
		return nil, nil, nil, err
	}
	return cams, presets, prev, nil
}

func readCameras(db *sql.DB) ([]storage.Camera, error) {
	ok, err := hasTable(db, "cameras")
	if err != nil || !ok {
		return nil, err
	}

	rows, err := db.Query("SELECT label, ip, port FROM cameras ORDER BY label")
	if err != nil {
		return nil, fmt.Errorf("reading cameras: %w", err)
	}
	defer rows.Close()

	var out []storage.Camera
	for rows.Next() {
		var c storage.Camera
		if err := rows.Scan(&c.Label, &c.IP, &c.Port); err != nil {
			return nil, fmt.Errorf("reading cameras: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// readPresets handles both shapes of the presets table.
//
// The per-camera shape keys labels by (camera_label, number). The older global
// shape keys them by number alone, meaning one set shared by every camera; that
// set is fanned out to each camera here, matching what storage.Open does when
// it upgrades a database in place.
func readPresets(db *sql.DB, cams []storage.Camera) ([]legacyPreset, error) {
	cols, err := tableColumns(db, "presets")
	if err != nil || len(cols) == 0 {
		return nil, err
	}

	if cols["camera_label"] {
		rows, err := db.Query(
			"SELECT camera_label, number, label FROM presets ORDER BY camera_label, number")
		if err != nil {
			return nil, fmt.Errorf("reading presets: %w", err)
		}
		defer rows.Close()

		var out []legacyPreset
		for rows.Next() {
			var p legacyPreset
			if err := rows.Scan(&p.camera, &p.number, &p.label); err != nil {
				return nil, fmt.Errorf("reading presets: %w", err)
			}
			out = append(out, p)
		}
		return out, rows.Err()
	}

	rows, err := db.Query("SELECT number, label FROM presets ORDER BY number")
	if err != nil {
		return nil, fmt.Errorf("reading presets: %w", err)
	}
	defer rows.Close()

	var global []legacyPreset
	for rows.Next() {
		var p legacyPreset
		if err := rows.Scan(&p.number, &p.label); err != nil {
			return nil, fmt.Errorf("reading presets: %w", err)
		}
		global = append(global, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []legacyPreset
	for _, cam := range cams {
		for _, p := range global {
			out = append(out, legacyPreset{camera: cam.Label, number: p.number, label: p.label})
		}
	}
	// With no cameras there is nothing to key the labels by, and the slots were
	// unreachable anyway — recall needs a connection — so they are simply dropped.
	switch {
	case len(global) == 0:
	case len(cams) == 0:
		log.Printf("legacy database has %d shared preset label(s) but no cameras; discarding them", len(global))
	default:
		log.Printf("legacy database has one shared preset set; applying it to all %d camera(s)", len(cams))
	}
	return out, nil
}

func readPreviewSettings(db *sql.DB) (*storage.PreviewSettings, error) {
	ok, err := hasTable(db, "preview_settings")
	if err != nil || !ok {
		return nil, err
	}

	var ps storage.PreviewSettings
	err = db.QueryRow(
		"SELECT enable_ndi, enable_obs, enable_http, enable_rtsp, obs_ws_host, obs_ws_password FROM preview_settings WHERE id = 1",
	).Scan(&ps.EnableNDI, &ps.EnableOBS, &ps.EnableHTTP, &ps.EnableRTSP, &ps.OBSWSHost, &ps.OBSWSPassword)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // never configured; the new database keeps its defaults
	}
	if err != nil {
		return nil, fmt.Errorf("reading preview settings: %w", err)
	}
	return &ps, nil
}

func hasTable(db *sql.DB, name string) (bool, error) {
	cols, err := tableColumns(db, name)
	return len(cols) > 0, err
}

func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(
		"SELECT column_name FROM information_schema.columns WHERE table_name = ?", table)
	if err != nil {
		return nil, fmt.Errorf("inspecting %s: %w", table, err)
	}
	defer rows.Close()

	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("inspecting %s: %w", table, err)
		}
		cols[name] = true
	}
	return cols, rows.Err()
}
