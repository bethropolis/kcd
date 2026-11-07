package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAPIDropInWritesNestedValuesAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kcd.toml.d", "90-ipc.toml")
	if err := WriteAPIDropIn(path, map[string]any{
		"log_level": "debug",
		"share":     map[string]any{"accept": "ask"},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "log_level = \"debug\"") || !strings.Contains(text, "accept = \"ask\"") {
		t.Fatalf("drop-in = %q", text)
	}
	if mode := apiFileMode(t, path); mode&0077 != 0 {
		t.Fatalf("drop-in mode = %o", mode)
	}
}

func apiFileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}
