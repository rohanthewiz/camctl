package storage

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// migrateJSON imports data from old JSON config files if they exist,
// then renames them to .json.migrated so the migration runs only once.
func (d *DB) migrateJSON(dir string) {
	d.migrateCamerasJSON(filepath.Join(dir, "cameras.json"))
	d.migratePresetsJSON(filepath.Join(dir, "presets.json"))
}

func (d *DB) migrateCamerasJSON(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // file doesn't exist or unreadable — skip
	}

	var cams []Camera
	if err := json.Unmarshal(data, &cams); err != nil {
		log.Printf("storage: migrate %s: %v", path, err)
		return
	}

	// upsertCameraRow, not UpsertCamera: preset provisioning must wait until
	// after fanOutLegacyPresets has had a chance to apply the old shared labels.
	for _, cam := range cams {
		if err := d.upsertCameraRow(cam); err != nil {
			log.Printf("storage: migrate camera %q: %v", cam.Label, err)
		}
	}

	if err := os.Rename(path, path+".migrated"); err != nil {
		log.Printf("storage: rename %s: %v", path, err)
	} else {
		log.Printf("storage: migrated %d camera(s) from %s", len(cams), path)
	}
}

// migratePresetsJSON imports the old presets.json, which predates per-camera
// presets and therefore holds a single global set of labels. The rows land in
// the legacy holding table and Open's fanOutLegacyPresets copies them onto each
// saved camera — the same path taken by an old DuckDB presets table.
func (d *DB) migratePresetsJSON(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var presets []Preset
	if err := json.Unmarshal(data, &presets); err != nil {
		log.Printf("storage: migrate %s: %v", path, err)
		return
	}

	if _, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS ` + legacyPresetsTable + ` (
			number INTEGER PRIMARY KEY,
			label  TEXT NOT NULL
		)`); err != nil {
		log.Printf("storage: create %s: %v", legacyPresetsTable, err)
		return
	}

	for _, p := range presets {
		_, err := d.db.Exec(
			"INSERT OR REPLACE INTO "+legacyPresetsTable+" (number, label) VALUES (?, ?)",
			p.Number, p.Label,
		)
		if err != nil {
			log.Printf("storage: migrate preset %d: %v", p.Number, err)
		}
	}

	if err := os.Rename(path, path+".migrated"); err != nil {
		log.Printf("storage: rename %s: %v", path, err)
	} else {
		log.Printf("storage: migrated %d preset(s) from %s", len(presets), path)
	}
}
