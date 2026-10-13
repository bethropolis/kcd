package device

import (
	"os"
	"path/filepath"
	"testing"
)

// Same atomicity contract as config.Save: only complete device lists land,
// at 0600, with no temp files left behind.
func TestSaveDevicesIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.json")

	infos := []DeviceInfo{{ID: "a", Name: "Phone", Type: "phone", State: StatePaired}}
	if err := SaveDevices(path, infos); err != nil {
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

	loaded, err := LoadDevices(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].ID != "a" {
		t.Errorf("round trip = %v, want the one saved device", loaded)
	}
}
