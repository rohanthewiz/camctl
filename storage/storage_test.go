package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// openTestDB creates a DB in a fresh temp dir. Each test gets its own
// directory so DuckDB file locks and JSON-migration side effects can't
// leak between tests.
func openTestDB(t *testing.T) (*DB, string) {
	t.Helper()
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d, dir
}

// mustPresets reads a camera's presets or fails the test.
func mustPresets(t *testing.T, d *DB, cameraLabel string) []Preset {
	t.Helper()
	presets, err := d.AllPresets(cameraLabel)
	if err != nil {
		t.Fatalf("AllPresets(%q): %v", cameraLabel, err)
	}
	return presets
}

func TestOpenSeedsDefaults(t *testing.T) {
	d, _ := openTestDB(t)

	presets, err := d.AllPresets("Main")
	if err != nil {
		t.Fatalf("AllPresets: %v", err)
	}
	if len(presets) != presetCount {
		t.Fatalf("seeded %d presets, want %d", len(presets), presetCount)
	}
	// Slots are 0-based internally but labeled 1-based for the UI.
	if presets[0].Number != 0 || presets[0].Label != "Preset 1" {
		t.Errorf("first preset = %+v, want {0 Preset 1}", presets[0])
	}

	// With no camera selected the grid still needs six slots to render.
	if got := len(mustPresets(t, d, "")); got != presetCount {
		t.Errorf("AllPresets(\"\") returned %d slots, want %d", got, presetCount)
	}

	ps, err := d.GetPreviewSettings()
	if err != nil {
		t.Fatalf("GetPreviewSettings: %v", err)
	}
	// Default preview config: only NDI enabled — the other strategies need
	// extra software (OBS, ffmpeg) or camera-specific endpoints.
	want := PreviewSettings{EnableNDI: true}
	if ps != want {
		t.Errorf("default preview settings = %+v, want %+v", ps, want)
	}
}

// TestReopenIsIdempotent guards the seed logic: reopening an existing DB
// must not duplicate presets or reset user data.
func TestReopenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	d, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	// The camera must be saved for its presets to survive: Open prunes preset
	// rows belonging to no known camera.
	if err := d.UpsertCamera(Camera{Label: "Main", IP: "10.0.0.1", Port: 52381}); err != nil {
		t.Fatalf("UpsertCamera: %v", err)
	}
	if err := d.UpdatePresetLabel("Main", 2, "Wide Shot"); err != nil {
		t.Fatalf("UpdatePresetLabel: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	d2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer d2.Close()

	presets := mustPresets(t, d2, "Main")
	if len(presets) != presetCount {
		t.Fatalf("after reopen: %d presets, want %d", len(presets), presetCount)
	}
	if presets[2].Label != "Wide Shot" {
		t.Errorf("preset 2 label = %q, want %q (user edit lost on reopen)", presets[2].Label, "Wide Shot")
	}
}

// TestPresetsAreScopedPerCamera is the regression guard for the original bug:
// a second camera used to show the first camera's preset labels because presets
// were keyed by slot number alone.
func TestPresetsAreScopedPerCamera(t *testing.T) {
	d, _ := openTestDB(t)

	for _, cam := range []Camera{
		{Label: "Stage Left", IP: "10.0.0.1", Port: 52381},
		{Label: "Balcony", IP: "10.0.0.2", Port: 52381},
	} {
		if err := d.UpsertCamera(cam); err != nil {
			t.Fatalf("UpsertCamera(%q): %v", cam.Label, err)
		}
	}

	if err := d.UpdatePresetLabel("Stage Left", 0, "Pulpit"); err != nil {
		t.Fatalf("UpdatePresetLabel: %v", err)
	}

	// The second camera must start from placeholders, not inherit "Pulpit".
	if got := mustPresets(t, d, "Balcony")[0].Label; got != "Preset 1" {
		t.Errorf("Balcony slot 0 = %q, want %q — presets leaked across cameras", got, "Preset 1")
	}

	// Same slot number, different camera: both labels coexist.
	if err := d.UpdatePresetLabel("Balcony", 0, "Wide"); err != nil {
		t.Fatalf("UpdatePresetLabel: %v", err)
	}
	if got := mustPresets(t, d, "Stage Left")[0].Label; got != "Pulpit" {
		t.Errorf("Stage Left slot 0 = %q, want %q — overwritten by the other camera", got, "Pulpit")
	}

	// A rename carries the camera's presets across to the new label.
	if err := d.UpdateCamera("Stage Left", Camera{Label: "Stage L", IP: "10.0.0.1", Port: 52381}); err != nil {
		t.Fatalf("UpdateCamera rename: %v", err)
	}
	if got := mustPresets(t, d, "Stage L")[0].Label; got != "Pulpit" {
		t.Errorf("after rename slot 0 = %q, want %q", got, "Pulpit")
	}

	// Deleting a camera takes its preset labels with it — a later camera
	// reusing the name must not resurrect them.
	if err := d.RemoveCamera("Balcony"); err != nil {
		t.Fatalf("RemoveCamera: %v", err)
	}
	if err := d.UpsertCamera(Camera{Label: "Balcony", IP: "10.0.0.9", Port: 52381}); err != nil {
		t.Fatalf("UpsertCamera: %v", err)
	}
	if got := mustPresets(t, d, "Balcony")[0].Label; got != "Preset 1" {
		t.Errorf("re-added Balcony slot 0 = %q, want %q — stale labels survived deletion", got, "Preset 1")
	}
}

// TestLegacyGlobalPresetsMigrate covers the upgrade from the single shared
// preset set: every saved camera inherits the old labels as its own starting
// point, and the holding table is cleaned up so it can't re-apply later.
func TestLegacyGlobalPresetsMigrate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Build a database in the old shape by hand.
	raw, err := sql.Open("duckdb", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	_, err = raw.Exec(`
		CREATE TABLE cameras (label TEXT PRIMARY KEY, ip TEXT NOT NULL, port INTEGER NOT NULL);
		CREATE TABLE presets (number INTEGER PRIMARY KEY, label TEXT NOT NULL);
		INSERT INTO cameras VALUES ('Main', '10.0.0.1', 52381), ('Side', '10.0.0.2', 52381);
		INSERT INTO presets VALUES (0, 'Pulpit'), (1, 'Choir');
	`)
	if err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	d, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	// Both cameras start from the old shared labels, topped up to a full set.
	for _, cam := range []string{"Main", "Side"} {
		presets := mustPresets(t, d, cam)
		if len(presets) != presetCount {
			t.Fatalf("%s: %d presets, want %d", cam, len(presets), presetCount)
		}
		if presets[0].Label != "Pulpit" || presets[1].Label != "Choir" {
			t.Errorf("%s: legacy labels lost: %+v", cam, presets[:2])
		}
		if presets[2].Label != "Preset 3" {
			t.Errorf("%s: slot 2 = %q, want placeholder", cam, presets[2].Label)
		}
	}

	// Editing one camera must no longer touch the other.
	if err := d.UpdatePresetLabel("Main", 0, "Lectern"); err != nil {
		t.Fatalf("UpdatePresetLabel: %v", err)
	}
	if got := mustPresets(t, d, "Side")[0].Label; got != "Pulpit" {
		t.Errorf("Side slot 0 = %q, want %q — still sharing after migration", got, "Pulpit")
	}

	// The holding table must be gone so a reopen can't re-apply the old labels
	// over the edit above.
	exists, err := d.tableExists(legacyPresetsTable)
	if err != nil {
		t.Fatalf("tableExists: %v", err)
	}
	if exists {
		t.Errorf("%s still present after migration", legacyPresetsTable)
	}
}

func TestCameraCRUD(t *testing.T) {
	d, _ := openTestDB(t)

	cams, err := d.AllCameras()
	if err != nil {
		t.Fatalf("AllCameras: %v", err)
	}
	if len(cams) != 0 {
		t.Fatalf("fresh DB has %d cameras, want 0", len(cams))
	}

	// Insert out of label order to verify AllCameras sorts by label.
	for _, cam := range []Camera{
		{Label: "Stage Right", IP: "10.0.0.2", Port: 52381},
		{Label: "Main", IP: "10.0.0.1", Port: 52381},
	} {
		if err := d.UpsertCamera(cam); err != nil {
			t.Fatalf("UpsertCamera(%q): %v", cam.Label, err)
		}
	}

	cams, err = d.AllCameras()
	if err != nil {
		t.Fatalf("AllCameras: %v", err)
	}
	if len(cams) != 2 || cams[0].Label != "Main" || cams[1].Label != "Stage Right" {
		t.Fatalf("cameras = %+v, want [Main, Stage Right] ordered by label", cams)
	}

	// Upsert with an existing label replaces rather than duplicates.
	if err := d.UpsertCamera(Camera{Label: "Main", IP: "10.0.0.99", Port: 1259}); err != nil {
		t.Fatalf("UpsertCamera replace: %v", err)
	}
	cams, _ = d.AllCameras()
	if len(cams) != 2 || cams[0].IP != "10.0.0.99" || cams[0].Port != 1259 {
		t.Fatalf("after replace: %+v, want Main updated in place", cams)
	}

	if err := d.RemoveCamera("Main"); err != nil {
		t.Fatalf("RemoveCamera: %v", err)
	}
	// Removing a nonexistent camera is defined as a no-op, not an error.
	if err := d.RemoveCamera("Ghost"); err != nil {
		t.Errorf("RemoveCamera on missing label: %v, want nil", err)
	}
	cams, _ = d.AllCameras()
	if len(cams) != 1 || cams[0].Label != "Stage Right" {
		t.Fatalf("after remove: %+v, want only Stage Right", cams)
	}
}

func TestUpdateCamera(t *testing.T) {
	d, _ := openTestDB(t)
	if err := d.UpsertCamera(Camera{Label: "Main", IP: "10.0.0.1", Port: 52381}); err != nil {
		t.Fatalf("UpsertCamera: %v", err)
	}

	t.Run("same label updates in place", func(t *testing.T) {
		if err := d.UpdateCamera("Main", Camera{Label: "Main", IP: "10.0.0.5", Port: 5678}); err != nil {
			t.Fatalf("UpdateCamera: %v", err)
		}
		cams, _ := d.AllCameras()
		if len(cams) != 1 || cams[0].IP != "10.0.0.5" || cams[0].Port != 5678 {
			t.Fatalf("cameras = %+v", cams)
		}
	})

	t.Run("rename is transactional", func(t *testing.T) {
		if err := d.UpdateCamera("Main", Camera{Label: "Podium", IP: "10.0.0.5", Port: 5678}); err != nil {
			t.Fatalf("UpdateCamera rename: %v", err)
		}
		cams, _ := d.AllCameras()
		if len(cams) != 1 || cams[0].Label != "Podium" {
			t.Fatalf("after rename: %+v, want single camera Podium", cams)
		}
	})

	t.Run("missing camera errors", func(t *testing.T) {
		if err := d.UpdateCamera("Nope", Camera{Label: "Nope", IP: "1.2.3.4", Port: 1}); err == nil {
			t.Error("update of missing label (same name): want error, got nil")
		}
		if err := d.UpdateCamera("Nope", Camera{Label: "Renamed", IP: "1.2.3.4", Port: 1}); err == nil {
			t.Error("rename of missing label: want error, got nil")
		}
	})
}

func TestUpdatePresetLabel(t *testing.T) {
	d, _ := openTestDB(t)

	if err := d.UpdatePresetLabel("Main", 1, "Close Up"); err != nil {
		t.Fatalf("UpdatePresetLabel: %v", err)
	}
	presets := mustPresets(t, d, "Main")
	if presets[1].Label != "Close Up" {
		t.Errorf("preset 1 label = %q, want %q", presets[1].Label, "Close Up")
	}

	// Out-of-range slots must be rejected, not silently ignored.
	if err := d.UpdatePresetLabel("Main", -1, "x"); err == nil {
		t.Error("negative slot: want error, got nil")
	}
	if err := d.UpdatePresetLabel("Main", presetCount, "x"); err == nil {
		t.Error("slot beyond presetCount: want error, got nil")
	}
	// Presets are per-camera, so there is nowhere to store a label without one.
	if err := d.UpdatePresetLabel("", 0, "x"); err == nil {
		t.Error("empty camera label: want error, got nil")
	}
}

func TestPreviewSettingsRoundtrip(t *testing.T) {
	d, _ := openTestDB(t)

	want := PreviewSettings{
		EnableNDI:     false,
		EnableOBS:     true,
		EnableHTTP:    true,
		EnableRTSP:    false,
		OBSWSHost:     "studio.local:4455",
		OBSWSPassword: "s3cret",
	}
	if err := d.UpdatePreviewSettings(want); err != nil {
		t.Fatalf("UpdatePreviewSettings: %v", err)
	}
	got, err := d.GetPreviewSettings()
	if err != nil {
		t.Fatalf("GetPreviewSettings: %v", err)
	}
	if got != want {
		t.Errorf("roundtrip = %+v, want %+v", got, want)
	}
}

// TestJSONMigration covers the one-time import of the pre-DuckDB config
// files: data lands in the DB and the files are renamed so a second Open
// doesn't re-import over newer edits.
func TestJSONMigration(t *testing.T) {
	dir := t.TempDir()
	camerasPath := filepath.Join(dir, "cameras.json")
	presetsPath := filepath.Join(dir, "presets.json")

	camerasJSON := `[{"Label":"Legacy Cam","IP":"192.168.1.50","Port":52381}]`
	presetsJSON := `[{"Number":0,"Label":"Pulpit"},{"Number":3,"Label":"Choir"}]`
	if err := os.WriteFile(camerasPath, []byte(camerasJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(presetsPath, []byte(presetsJSON), 0644); err != nil {
		t.Fatal(err)
	}

	d, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	cams, err := d.AllCameras()
	if err != nil {
		t.Fatalf("AllCameras: %v", err)
	}
	if len(cams) != 1 || cams[0].Label != "Legacy Cam" || cams[0].IP != "192.168.1.50" {
		t.Errorf("migrated cameras = %+v", cams)
	}

	// presets.json predates per-camera presets, so its single global set is
	// applied to each migrated camera; lazy seeding then tops the sparse slot
	// numbers (0 and 3 here) up to a full set.
	presets := mustPresets(t, d, "Legacy Cam")
	if len(presets) != presetCount {
		t.Fatalf("%d presets after migration, want %d", len(presets), presetCount)
	}
	if presets[0].Label != "Pulpit" || presets[3].Label != "Choir" {
		t.Errorf("migrated preset labels lost: %+v", presets)
	}
	if presets[1].Label != "Preset 2" {
		t.Errorf("gap slot 1 = %q, want placeholder", presets[1].Label)
	}

	// Originals must be renamed so migration can't run twice.
	for _, p := range []string{camerasPath, presetsPath} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists — migration would re-run on next open", p)
		}
		if _, err := os.Stat(p + ".migrated"); err != nil {
			t.Errorf("%s.migrated missing: %v", p, err)
		}
	}
}
