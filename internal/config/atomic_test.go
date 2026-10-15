package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A crash mid-write must never leave a truncation: the temp file renames
// into place, so only complete configs land. Permissions stay 0600 and no
// temp files linger.
func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kcd.toml")

	cfg := Defaults()
	cfg.DeviceID = "test-device"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", st.Mode().Perm())
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.DeviceID != "test-device" {
		t.Errorf("DeviceID = %q, want test-device", reloaded.DeviceID)
	}
}
