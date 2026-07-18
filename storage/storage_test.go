package storage

import (
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

func TestOpenSeedsDefaults(t *testing.T) {
	d, _ := openTestDB(t)

	presets, err := d.AllPresets()
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
	if err := d.UpdatePresetLabel(2, "Wide Shot"); err != nil {
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

	presets, err := d2.AllPresets()
	if err != nil {
		t.Fatalf("AllPresets: %v", err)
	}
	if len(presets) != presetCount {
		t.Fatalf("after reopen: %d presets, want %d", len(presets), presetCount)
	}
	if presets[2].Label != "Wide Shot" {
		t.Errorf("preset 2 label = %q, want %q (user edit lost on reopen)", presets[2].Label, "Wide Shot")
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

	if err := d.UpdatePresetLabel(1, "Close Up"); err != nil {
		t.Fatalf("UpdatePresetLabel: %v", err)
	}
	presets, _ := d.AllPresets()
	if presets[1].Label != "Close Up" {
		t.Errorf("preset 1 label = %q, want %q", presets[1].Label, "Close Up")
	}

	// Out-of-range slots must be rejected, not silently ignored.
	if err := d.UpdatePresetLabel(-1, "x"); err == nil {
		t.Error("negative slot: want error, got nil")
	}
	if err := d.UpdatePresetLabel(presetCount, "x"); err == nil {
		t.Error("slot beyond presetCount: want error, got nil")
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

	presets, err := d.AllPresets()
	if err != nil {
		t.Fatalf("AllPresets: %v", err)
	}
	// Migration runs before seeding, and seeding tops up to presetCount
	// without touching migrated rows.
	if len(presets) != presetCount {
		t.Fatalf("%d presets after migration, want %d", len(presets), presetCount)
	}
	if presets[0].Label != "Pulpit" || presets[3].Label != "Choir" {
		t.Errorf("migrated preset labels lost: %+v", presets)
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
