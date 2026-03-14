package storage

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "github.com/duckdb/duckdb-go/v2"
)

const presetCount = 6

// DB wraps a DuckDB connection for camera and preset persistence.
type DB struct {
	db *sql.DB
}

// Camera stores a named camera connection target.
type Camera struct {
	Label string
	IP    string
	Port  int
}

// Preset holds a camera preset slot number and its user-defined label.
type Preset struct {
	Number int
	Label  string
}

// Open opens (or creates) a DuckDB database at dbPath and initializes the schema.
// If old JSON config files exist alongside the DB, their data is migrated automatically.
func Open(dbPath string) (*DB, error) {
	sqlDB, err := sql.Open("duckdb", dbPath)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", dbPath, err)
	}

	d := &DB{db: sqlDB}

	if err := d.createTables(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("storage: create tables: %w", err)
	}

	// Migrate any existing JSON files (one-time, non-fatal).
	dir := filepath.Dir(dbPath)
	if dir == "" || dir == "." {
		dir = "."
	}
	d.migrateJSON(dir)

	// Seed default presets if the table is empty.
	if err := d.seedPresets(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("storage: seed presets: %w", err)
	}

	return d, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) createTables() error {
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS cameras (
			label TEXT PRIMARY KEY,
			ip    TEXT NOT NULL,
			port  INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS presets (
			number INTEGER PRIMARY KEY,
			label  TEXT NOT NULL
		);
	`)
	return err
}

func (d *DB) seedPresets() error {
	var count int
	if err := d.db.QueryRow("SELECT COUNT(*) FROM presets").Scan(&count); err != nil {
		return err
	}
	if count >= presetCount {
		return nil
	}
	// Insert any missing preset slots.
	for i := count; i < presetCount; i++ {
		_, err := d.db.Exec(
			"INSERT OR IGNORE INTO presets (number, label) VALUES (?, ?)",
			i, fmt.Sprintf("Preset %d", i+1),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

// --- Camera operations ---

// AllCameras returns all saved cameras ordered by label.
func (d *DB) AllCameras() ([]Camera, error) {
	rows, err := d.db.Query("SELECT label, ip, port FROM cameras ORDER BY label")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Camera
	for rows.Next() {
		var c Camera
		if err := rows.Scan(&c.Label, &c.IP, &c.Port); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertCamera inserts a new camera or updates the existing entry with the same label.
func (d *DB) UpsertCamera(cam Camera) error {
	_, err := d.db.Exec(
		"INSERT OR REPLACE INTO cameras (label, ip, port) VALUES (?, ?, ?)",
		cam.Label, cam.IP, cam.Port,
	)
	return err
}

// UpdateCamera replaces the camera identified by oldLabel with new data.
// Uses a transaction when the label is being renamed.
func (d *DB) UpdateCamera(oldLabel string, cam Camera) error {
	if oldLabel == cam.Label {
		res, err := d.db.Exec(
			"UPDATE cameras SET ip = ?, port = ? WHERE label = ?",
			cam.IP, cam.Port, oldLabel,
		)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("camera %q not found", oldLabel)
		}
		return nil
	}

	// Label rename: delete old, insert new within a transaction.
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec("DELETE FROM cameras WHERE label = ?", oldLabel)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("camera %q not found", oldLabel)
	}
	_, err = tx.Exec(
		"INSERT INTO cameras (label, ip, port) VALUES (?, ?, ?)",
		cam.Label, cam.IP, cam.Port,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveCamera deletes the camera with the given label (no-op if not found).
func (d *DB) RemoveCamera(label string) error {
	_, err := d.db.Exec("DELETE FROM cameras WHERE label = ?", label)
	return err
}

// --- Preset operations ---

// AllPresets returns all presets ordered by slot number.
func (d *DB) AllPresets() ([]Preset, error) {
	rows, err := d.db.Query("SELECT number, label FROM presets ORDER BY number")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Preset
	for rows.Next() {
		var p Preset
		if err := rows.Scan(&p.Number, &p.Label); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdatePresetLabel changes the label for a preset by its slot number.
func (d *DB) UpdatePresetLabel(num int, label string) error {
	if num < 0 || num >= presetCount {
		return fmt.Errorf("storage: invalid preset number %d", num)
	}
	_, err := d.db.Exec("UPDATE presets SET label = ? WHERE number = ?", label, num)
	return err
}
