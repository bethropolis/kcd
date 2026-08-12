package mpris

import (
	"hash/fnv"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"go.uber.org/zap"
)

// maxAlbumArtBytes caps inbound album art payloads (matches the 5 MiB
// limit in the KDE Connect reference AlbumArtCache).
const maxAlbumArtBytes = 5 * 1024 * 1024

// maxArtCacheFiles bounds the on-disk cache. Exceeding it clears the
// directory, mirroring the reference implementation's startup purge.
const maxArtCacheFiles = 500

// ArtCache resolves kdeconnect:/artUri album art URIs to local file://
// URLs. Bytes fetched from the phone are streamed to
// $XDG_CACHE_HOME/kcd/art/<kdeArtHash>.<ext>.
type ArtCache struct {
	dir      string
	mu       sync.RWMutex
	resolved map[string]string // raw albumArtUrl -> file:// path
}

// NewArtCache creates the cache directory and returns an empty cache.
func NewArtCache(logger *zap.Logger) *ArtCache {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = filepath.Join(os.TempDir(), "kcd-cache")
	}
	dir := filepath.Join(base, "kcd", "art")
	if err := os.MkdirAll(dir, 0700); err != nil {
		logger.Warn("mpris: failed to create album art cache dir",
			zap.String("path", dir), zap.Error(err))
	}
	purgeArtCacheDir(dir, logger)
	return &ArtCache{
		dir:      dir,
		resolved: make(map[string]string),
	}
}

// purgeArtCacheDir clears the cache when it grows past maxArtCacheFiles.
func purgeArtCacheDir(dir string, logger *zap.Logger) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	if len(entries) <= maxArtCacheFiles {
		return
	}
	logger.Debug("mpris: clearing oversized album art cache",
		zap.Int("files", len(entries)))
	for _, e := range entries {
		if !e.IsDir() {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// Dir returns the on-disk cache directory.
func (c *ArtCache) Dir() string {
	return c.dir
}

// Key derives the cache filename stem for an album art URI: the
// kdeArtHash query parameter when present, otherwise an FNV-1a hash of
// the full URI (matching the reference qHash-based naming).
func (c *ArtCache) Key(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil {
		if h := u.Query().Get("kdeArtHash"); h != "" {
			return h
		}
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(rawURL))
	return strconv.FormatUint(uint64(h.Sum32()), 10)
}

// Resolve returns a file:// URL for a cached album art URI, or "" when
// the art has not been downloaded (or is still in flight).
func (c *ArtCache) Resolve(rawURL string) string {
	if !strings.HasPrefix(rawURL, "kdeconnect:/") {
		return ""
	}
	c.mu.RLock()
	path, ok := c.resolved[rawURL]
	c.mu.RUnlock()
	if ok {
		return path
	}

	// Disk fallback: a previously cached file survives a daemon restart.
	matches, err := filepath.Glob(filepath.Join(c.dir, c.Key(rawURL)+".*"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	path = "file://" + matches[0]
	c.mu.Lock()
	c.resolved[rawURL] = path
	c.mu.Unlock()
	return path
}

// Commit moves a streamed temp file into the cache, sniffing its type
// from the first bytes, and returns the file:// URL for rawURL.
func (c *ArtCache) Commit(rawURL, tmpPath string) (string, error) {
	f, err := os.Open(tmpPath)
	if err != nil {
		return "", err
	}
	buf := make([]byte, 16)
	n, _ := io.ReadFull(f, buf)
	f.Close()

	ext := sniffImageExt(buf[:n])
	dst := filepath.Join(c.dir, c.Key(rawURL)+ext)
	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(dst)
		if err := os.Rename(tmpPath, dst); err != nil {
			return "", err
		}
	}

	fileURL := "file://" + dst
	c.mu.Lock()
	c.resolved[rawURL] = fileURL
	c.mu.Unlock()
	return fileURL, nil
}

// sniffImageExt guesses the file extension from image magic bytes.
// Falls back to .jpg, matching the reference implementation.
func sniffImageExt(data []byte) string {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return ".jpg"
	case len(data) >= 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G':
		return ".png"
	case len(data) >= 6 && string(data[:4]) == "GIF8":
		return ".gif"
	case len(data) >= 12 && string(data[8:12]) == "WEBP":
		return ".webp"
	default:
		return ".jpg"
	}
}
