package mpris

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSniffImageExt(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00}, ".jpg"},
		{"png", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, ".png"},
		{"gif", []byte("GIF89a..."), ".gif"},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), ".webp"},
		{"unknown-falls-back-jpg", []byte("not an image"), ".jpg"},
		{"empty", nil, ".jpg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sniffImageExt(tc.data); got != tc.want {
				t.Fatalf("sniffImageExt(%v) = %q, want %q", tc.data, got, tc.want)
			}
		})
	}
}

func TestArtCacheKey(t *testing.T) {
	c := &ArtCache{}
	// kdeArtHash query param wins.
	got := c.Key("kdeconnect:/artUri?title=Test&kdeArtHash=1141556203")
	if got != "1141556203" {
		t.Fatalf("Key with kdeArtHash = %q, want 1141556203", got)
	}
	// No kdeArtHash -> stable FNV fallback, same for identical URLs.
	a := c.Key("kdeconnect:/artUri?title=OnlyTitle")
	b := c.Key("kdeconnect:/artUri?title=OnlyTitle")
	if a == "" || a != b {
		t.Fatalf("expected stable non-empty key, got %q / %q", a, b)
	}
	// Different URLs -> different keys (extremely unlikely to collide).
	c2 := c.Key("kdeconnect:/artUri?title=Other")
	if a == c2 {
		t.Fatalf("expected different keys, both %q", a)
	}
}

func TestArtCacheResolveAndCommit(t *testing.T) {
	dir := t.TempDir()
	c := &ArtCache{dir: dir, resolved: make(map[string]string)}

	rawURL := "kdeconnect:/artUri?title=Test&kdeArtHash=42"
	if got := c.Resolve(rawURL); got != "" {
		t.Fatalf("expected miss before commit, got %q", got)
	}

	// Stream a tiny JPEG into a temp file, then commit.
	tmp := filepath.Join(dir, "tmp-art")
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00}
	if err := os.WriteFile(tmp, png, 0600); err != nil {
		t.Fatal(err)
	}

	fileURL, err := c.Commit(rawURL, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fileURL, "file://") {
		t.Fatalf("expected file:// prefix, got %q", fileURL)
	}
	path := strings.TrimPrefix(fileURL, "file://")
	if !strings.HasSuffix(path, "42.png") {
		t.Fatalf("expected extension sniffed to .png with key 42, got %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cached file missing: %v", err)
	}

	if got := c.Resolve(rawURL); got != fileURL {
		t.Fatalf("expected resolve to return %q, got %q", fileURL, got)
	}

	// Disk fallback: a fresh cache (restart) finds the file again.
	c2 := &ArtCache{dir: dir, resolved: make(map[string]string)}
	if got := c2.Resolve(rawURL); got != fileURL {
		t.Fatalf("expected disk fallback resolve %q, got %q", fileURL, got)
	}
}

func TestArtCacheCommitRenamesTemp(t *testing.T) {
	dir := t.TempDir()
	c := &ArtCache{dir: dir, resolved: make(map[string]string)}

	tmp := filepath.Join(dir, "tmp-art")
	jpeg := bytes.Repeat([]byte{0xFF, 0xD8, 0xFF}, 32)
	if err := os.WriteFile(tmp, jpeg, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit("kdeconnect:/artUri?kdeArtHash=7", tmp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("expected temp file removed after commit, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "7.jpg")); err != nil {
		t.Fatalf("expected cached 7.jpg: %v", err)
	}
}
