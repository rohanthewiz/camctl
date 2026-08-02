package storage

import (
	"database/sql"
	"fmt"
	"log"
	"path/filepath"

	_ "github.com/duckdb/duckdb-go/v2"
)

const presetCount = 6

// legacyPresetsTable is the holding area for pre-per-camera preset rows during
// the schema upgrade. See migratePresetsSchema and fanOutLegacyPresets.
const legacyPresetsTable = "presets_legacy"

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
//
// Presets are scoped to a camera: the label is keyed by (camera_label, number)
// in the DB. The positions themselves live in the camera's own memory — VISCA
// preset set/recall address slots on the device — so slot 3 on "Stage Left" and
// slot 3 on "Balcony" are genuinely different shots and must not share a name.
type Preset struct {
	Number int
	Label  string
}

// defaultPresetLabel is the placeholder name for an unconfigured slot.
// Slots are 0-based internally but presented 1-based to the operator.
func defaultPresetLabel(num int) string {
	return fmt.Sprintf("Preset %d", num+1)
}

// DefaultPresets returns a full set of placeholder slots. Used to render the
// preset grid when no camera is selected — there is no camera to key real
// labels by, but the UI still needs six cards to lay out.
func DefaultPresets() []Preset {
	out := make([]Preset, presetCount)
	for i := range presetCount {
		out[i] = Preset{Number: i, Label: defaultPresetLabel(i)}
	}
	return out
}

// PreviewSettings stores which preview strategies are enabled
// and OBS WebSocket connection details.
type PreviewSettings struct {
	EnableNDI     bool
	EnableOBS     bool
	EnableHTTP    bool
	EnableRTSP    bool
	OBSWSHost     string
	OBSWSPassword string
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

	// Hand the old global preset labels to the saved cameras. This runs after
	// migrateJSON so that both sources of legacy rows — an old DB and an old
	// presets.json — are fanned out in one pass, and after the cameras table is
	// populated so there is something to fan out to.
	if err := d.fanOutLegacyPresets(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("storage: migrate presets to per-camera: %w", err)
	}

	// Drop preset rows whose camera no longer exists. Presets are seeded lazily
	// on read, so a camera that was typed into the add form but never saved (a
	// failed connection, say) can leave rows behind; without a cascading foreign
	// key this sweep is what keeps the table from accumulating them.
	if err := d.pruneOrphanPresets(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("storage: prune orphan presets: %w", err)
	}

	// Seed default preview settings (single-row config).
	if err := d.seedPreviewSettings(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("storage: seed preview settings: %w", err)
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
		CREATE TABLE IF NOT EXISTS preview_settings (
			id              INTEGER PRIMARY KEY DEFAULT 1,
			enable_ndi      BOOLEAN NOT NULL DEFAULT true,
			enable_obs      BOOLEAN NOT NULL DEFAULT false,
			enable_http     BOOLEAN NOT NULL DEFAULT false,
			enable_rtsp     BOOLEAN NOT NULL DEFAULT false,
			obs_ws_host     TEXT NOT NULL DEFAULT '',
			obs_ws_password TEXT NOT NULL DEFAULT ''
		);
	`)
	if err != nil {
		return err
	}

	// Get the old single-set presets table out of the way before creating the
	// per-camera one — CREATE TABLE IF NOT EXISTS would silently keep the old
	// shape and every later query would fail on the missing column.
	if err := d.migratePresetsSchema(); err != nil {
		return err
	}

	// No FOREIGN KEY to cameras: DuckDB does not support ON DELETE CASCADE, and
	// an un-cascaded FK would block camera deletion. The camera operations below
	// keep preset rows in step by hand (delete on remove, re-key on rename).
	_, err = d.db.Exec(`
		CREATE TABLE IF NOT EXISTS presets (
			camera_label TEXT    NOT NULL,
			number       INTEGER NOT NULL,
			label        TEXT    NOT NULL,
			PRIMARY KEY (camera_label, number)
		);
	`)
	return err
}

// migratePresetsSchema upgrades a pre-existing presets table keyed by slot
// number alone to the per-camera schema. Under the old shape every camera read
// the same six labels, so adding a second camera showed the first camera's
// preset names.
//
//	presets(number PK, label)  ->  presets_legacy(number PK, label)
//	                               presets(camera_label, number PK, label)
//
// The rows are parked rather than dropped; fanOutLegacyPresets copies them to
// each saved camera once the camera list is known. Renaming (instead of
// ALTER TABLE) keeps the new table definition in one place and lets every other
// query assume the new schema unconditionally.
func (d *DB) migratePresetsSchema() error {
	cols, err := d.tableColumns("presets")
	if err != nil {
		return err
	}
	if len(cols) == 0 || cols["camera_label"] {
		return nil // no table yet, or already migrated
	}

	// A leftover holding table from an interrupted earlier migration would
	// collide with the rename, so clear it first.
	if _, err := d.db.Exec("DROP TABLE IF EXISTS " + legacyPresetsTable); err != nil {
		return err
	}
	if _, err := d.db.Exec("ALTER TABLE presets RENAME TO " + legacyPresetsTable); err != nil {
		return err
	}
	log.Printf("storage: upgrading presets to per-camera labels")
	return nil
}

// fanOutLegacyPresets copies the parked global preset labels onto every saved
// camera, then drops the holding table. INSERT OR IGNORE means a camera that
// already has its own labels keeps them.
//
// With no cameras saved there is nothing to attach the labels to — the slots
// were unreachable anyway, since recall requires a connection — so the holding
// table is simply dropped and each camera starts from placeholder names.
func (d *DB) fanOutLegacyPresets() error {
	exists, err := d.tableExists(legacyPresetsTable)
	if err != nil || !exists {
		return err
	}

	cams, err := d.AllCameras()
	if err != nil {
		return err
	}
	for _, cam := range cams {
		_, err := d.db.Exec(
			"INSERT OR IGNORE INTO presets (camera_label, number, label) "+
				"SELECT ?, number, label FROM "+legacyPresetsTable,
			cam.Label,
		)
		if err != nil {
			return err
		}
	}

	if _, err := d.db.Exec("DROP TABLE " + legacyPresetsTable); err != nil {
		return err
	}
	log.Printf("storage: applied legacy preset labels to %d camera(s)", len(cams))
	return nil
}

// pruneOrphanPresets deletes preset rows that no saved camera claims.
func (d *DB) pruneOrphanPresets() error {
	_, err := d.db.Exec(
		"DELETE FROM presets WHERE camera_label NOT IN (SELECT label FROM cameras)")
	return err
}

// tableColumns returns the column names of a table as a set.
// An empty (non-nil) map means the table does not exist.
func (d *DB) tableColumns(table string) (map[string]bool, error) {
	rows, err := d.db.Query(
		"SELECT column_name FROM information_schema.columns WHERE table_name = ?", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

func (d *DB) tableExists(table string) (bool, error) {
	var n int
	err := d.db.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_name = ?", table).Scan(&n)
	return n > 0, err
}

// ensurePresets tops up a camera's slots with placeholder labels.
//
// Seeding is lazy — done on read and on camera insert — rather than a separate
// provisioning step, so cameras that predate this schema (or arrive via JSON
// migration) also get a full set. Each slot is inserted individually with
// INSERT OR IGNORE because existing rows are not necessarily a contiguous
// prefix: JSON migration can import sparse slot numbers (e.g. only 0 and 3),
// which a count-based starting index would leave with gaps.
func (d *DB) ensurePresets(cameraLabel string) error {
	for i := range presetCount {
		_, err := d.db.Exec(
			"INSERT OR IGNORE INTO presets (camera_label, number, label) VALUES (?, ?, ?)",
			cameraLabel, i, defaultPresetLabel(i),
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
// The camera's own preset slots are provisioned at the same time so a freshly
// added camera starts with its own placeholder labels rather than inheriting
// another camera's names.
func (d *DB) UpsertCamera(cam Camera) error {
	if err := d.upsertCameraRow(cam); err != nil {
		return err
	}
	return d.ensurePresets(cam.Label)
}

// upsertCameraRow writes just the camera row, without provisioning presets.
//
// Kept separate for the JSON migration path: seeding placeholder labels there
// would occupy the slots that fanOutLegacyPresets is about to fill, and its
// INSERT OR IGNORE would then skip the user's real names. Lazy seeding on first
// read still guarantees a full set afterwards.
func (d *DB) upsertCameraRow(cam Camera) error {
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

	// Presets are keyed by camera label, so a rename has to carry them across in
	// the same transaction — otherwise the renamed camera would silently fall
	// back to placeholder labels and the old names would linger as orphans.
	// Any stale rows already sitting under the new label (from a camera deleted
	// outside this code path) are cleared first so the re-key can't hit the
	// composite primary key.
	if _, err := tx.Exec("DELETE FROM presets WHERE camera_label = ?", cam.Label); err != nil {
		return err
	}
	if _, err := tx.Exec(
		"UPDATE presets SET camera_label = ? WHERE camera_label = ?", cam.Label, oldLabel,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveCamera deletes the camera with the given label along with its preset
// labels (no-op if not found). The cascade is done by hand because DuckDB
// foreign keys cannot use ON DELETE CASCADE.
func (d *DB) RemoveCamera(label string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM cameras WHERE label = ?", label); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM presets WHERE camera_label = ?", label); err != nil {
		return err
	}
	return tx.Commit()
}

// --- Preset operations ---

// AllPresets returns one camera's presets ordered by slot number, seeding any
// missing slots with placeholder labels first.
//
// An empty cameraLabel means "no camera selected" — there is nothing to key
// labels by, so a placeholder set is returned rather than an error, letting the
// UI render its grid before the first connection.
func (d *DB) AllPresets(cameraLabel string) ([]Preset, error) {
	if cameraLabel == "" {
		return DefaultPresets(), nil
	}
	if err := d.ensurePresets(cameraLabel); err != nil {
		return nil, err
	}

	rows, err := d.db.Query(
		"SELECT number, label FROM presets WHERE camera_label = ? ORDER BY number", cameraLabel)
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

// UpdatePresetLabel changes the label of one slot on one camera.
//
// Upsert rather than UPDATE: the row is normally seeded already, but this keeps
// the write correct if a label edit is the first thing that touches a camera.
func (d *DB) UpdatePresetLabel(cameraLabel string, num int, label string) error {
	if cameraLabel == "" {
		return fmt.Errorf("storage: preset labels require a camera")
	}
	if num < 0 || num >= presetCount {
		return fmt.Errorf("storage: invalid preset number %d", num)
	}
	_, err := d.db.Exec(
		"INSERT OR REPLACE INTO presets (camera_label, number, label) VALUES (?, ?, ?)",
		cameraLabel, num, label,
	)
	return err
}

// --- Preview settings operations ---

// seedPreviewSettings inserts the default preview config row if absent.
// Only NDI Direct is enabled by default — other protocols require
// additional software or configuration.
func (d *DB) seedPreviewSettings() error {
	var count int
	if err := d.db.QueryRow("SELECT COUNT(*) FROM preview_settings").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := d.db.Exec(
		"INSERT INTO preview_settings (id, enable_ndi, enable_obs, enable_http, enable_rtsp, obs_ws_host, obs_ws_password) VALUES (1, true, false, false, false, '', '')",
	)
	return err
}

// GetPreviewSettings returns the current preview configuration.
func (d *DB) GetPreviewSettings() (PreviewSettings, error) {
	var ps PreviewSettings
	err := d.db.QueryRow(
		"SELECT enable_ndi, enable_obs, enable_http, enable_rtsp, obs_ws_host, obs_ws_password FROM preview_settings WHERE id = 1",
	).Scan(&ps.EnableNDI, &ps.EnableOBS, &ps.EnableHTTP, &ps.EnableRTSP, &ps.OBSWSHost, &ps.OBSWSPassword)
	return ps, err
}

// UpdatePreviewSettings persists the user's preview protocol preferences.
func (d *DB) UpdatePreviewSettings(ps PreviewSettings) error {
	_, err := d.db.Exec(
		"UPDATE preview_settings SET enable_ndi = ?, enable_obs = ?, enable_http = ?, enable_rtsp = ?, obs_ws_host = ?, obs_ws_password = ? WHERE id = 1",
		ps.EnableNDI, ps.EnableOBS, ps.EnableHTTP, ps.EnableRTSP, ps.OBSWSHost, ps.OBSWSPassword,
	)
	return err
}
