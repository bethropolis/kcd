package mpris

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// tinyPNG encodes a real w×h image for Commit inputs.
func tinyPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func commitFile(t *testing.T, c *ArtCache, rawURL string, data []byte) (string, error) {
	t.Helper()
	tmp := filepath.Join(c.dir, "tmp-art")
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		t.Fatal(err)
	}
	return c.Commit(rawURL, tmp)
}

func TestArtCacheResolveAndCommit(t *testing.T) {
	dir := t.TempDir()
	c := &ArtCache{dir: dir, resolved: make(map[string]string)}

	rawURL := "kdeconnect:/artUri?title=Test&kdeArtHash=42"
	if got := c.Resolve(rawURL); got != "" {
		t.Fatalf("expected miss before commit, got %q", got)
	}

	fileURL, err := commitFile(t, c, rawURL, tinyPNG(t, 4, 4))
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimPrefix(fileURL, "file://")
	if !strings.HasSuffix(path, "42.jpg") {
		t.Fatalf("expected re-encoded 42.jpg, got %q", path)
	}
	// The cache holds a decodable JPEG, not the peer's PNG bytes.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("cached file does not decode as JPEG: %v", err)
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

func TestArtCacheCommitCleansTemp(t *testing.T) {
	dir := t.TempDir()
	c := &ArtCache{dir: dir, resolved: make(map[string]string)}

	if _, err := commitFile(t, c, "kdeconnect:/artUri?kdeArtHash=7", tinyPNG(t, 2, 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tmp-art")); !os.IsNotExist(err) {
		t.Fatalf("expected temp file removed after commit, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "7.jpg")); err != nil {
		t.Fatalf("expected cached 7.jpg: %v", err)
	}
}

func pngChunk(dst *bytes.Buffer, typ string, data []byte) {
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[:4], uint32(len(data)))
	copy(hdr[4:], typ)
	dst.Write(hdr[:])
	dst.Write(data)
	sum := crc32.Checksum(append([]byte(typ), data...), crc32.MakeTable(crc32.IEEE))
	var tail [4]byte
	binary.BigEndian.PutUint32(tail[:], sum)
	dst.Write(tail[:])
}

// oversizePNG has a 4096x4096 header with no raster behind it: DecodeConfig
// reports the dimensions, so the gate must fire before any allocation.
func oversizePNG() []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], 4096)
	binary.BigEndian.PutUint32(ihdr[4:8], 4096)
	ihdr[8], ihdr[9] = 8, 2
	pngChunk(&b, "IHDR", ihdr[:])
	pngChunk(&b, "IDAT", nil)
	return b.Bytes()
}

func TestArtCacheRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	c := &ArtCache{dir: dir, resolved: make(map[string]string)}

	if _, err := commitFile(t, c, "kdeconnect:/artUri?kdeArtHash=9", oversizePNG()); err == nil {
		t.Fatal("expected rejection of 4096x4096 art, got nil error")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "9.*"))
	if len(matches) != 0 {
		t.Fatalf("rejected art left files behind: %v", matches)
	}
	if got := c.Resolve("kdeconnect:/artUri?kdeArtHash=9"); got != "" {
		t.Fatalf("rejected art resolved to %q", got)
	}
}

func TestArtCacheRejectsNonImage(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"text", []byte("not an image")},
		{"empty", nil},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			c := &ArtCache{dir: dir, resolved: make(map[string]string)}
			if _, err := commitFile(t, c, "kdeconnect:/artUri?kdeArtHash=5", tc.data); err == nil {
				t.Fatalf("expected rejection of %s input", tc.name)
			}
		})
	}
}

// textBombPNG is a 1x1 image carrying a large compressed zTXt chunk: tiny on
// the wire, huge once a retaining decoder inflates it. Go skips the chunk,
// but downstream decoders may not — Commit must store the re-encode, never
// these bytes.
func textBombPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], 1)
	binary.BigEndian.PutUint32(ihdr[4:8], 1)
	ihdr[8], ihdr[9] = 8, 2
	pngChunk(&b, "IHDR", ihdr[:])

	var zipped bytes.Buffer
	w, _ := zlib.NewWriterLevel(&zipped, zlib.BestCompression)
	if _, err := w.Write(bytes.Repeat([]byte("bombkeyword!"), 65536)); err != nil {
		t.Fatal(err)
	}
	w.Close()
	ztxt := append([]byte("bombkeyword\x00\x00"), zipped.Bytes()...)
	pngChunk(&b, "zTXt", ztxt)

	var raw bytes.Buffer
	raw.WriteByte(0)
	raw.Write([]byte{0x11, 0x22, 0x33})
	var idat bytes.Buffer
	w2, _ := zlib.NewWriterLevel(&idat, zlib.BestCompression)
	w2.Write(raw.Bytes())
	w2.Close()
	pngChunk(&b, "IDAT", idat.Bytes())
	pngChunk(&b, "IEND", nil)
	return b.Bytes()
}

func TestArtCacheStripsAncillaryChunks(t *testing.T) {
	dir := t.TempDir()
	c := &ArtCache{dir: dir, resolved: make(map[string]string)}

	fileURL, err := commitFile(t, c, "kdeconnect:/artUri?kdeArtHash=11", textBombPNG(t))
	if err != nil {
		t.Fatalf("valid-dimension bomb should re-encode, not reject: %v", err)
	}
	data, err := os.ReadFile(strings.TrimPrefix(fileURL, "file://"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("bombkeyword")) {
		t.Fatal("cached file still carries the peer's text chunk")
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("cached file does not decode as JPEG: %v", err)
	}
}
