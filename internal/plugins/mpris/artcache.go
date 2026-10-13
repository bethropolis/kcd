package mpris

import (
	"fmt"
	"hash/fnv"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/bethropolis/kcd/internal/log"
)

// Decoded-art bounds: rasters above 2048x2048 (16 MiB RGBA worst case)
// are rejected, never cached.

// maxAlbumArtBytes caps inbound album art payloads (matches the 5 MiB
// limit in the KDE Connect reference AlbumArtCache).
const maxAlbumArtBytes = 5 * 1024 * 1024

// maxArtDimension/maxArtPixels bound decoded album art: rasters above
// 2048x2048 (16 MiB RGBA worst case) are rejected, never cached.
const (
	maxArtDimension = 2048
	maxArtPixels    = 2048 * 2048
)

// maxArtCacheFiles bounds the on-disk cache. Exceeding it clears the
// directory, mirroring the reference implementation's startup purge.
const maxArtCacheFiles = 500

// ArtCache resolves kdeconnect:/ art URIs to local file:// URLs, streaming
// phone bytes into $XDG_CACHE_HOME/kcd/art/.
type ArtCache struct {
	dir      string
	mu       sync.RWMutex
	resolved map[string]string // raw albumArtUrl -> file:// path
}

// NewArtCache creates the cache directory and returns an empty cache.
func NewArtCache(logger log.Logger, cacheDirs ...string) *ArtCache {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = filepath.Join(os.TempDir(), "kcd-cache")
	}
	dir := filepath.Join(base, "kcd", "art")
	if len(cacheDirs) > 0 && cacheDirs[0] != "" {
		dir = cacheDirs[0]
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		logger.Warn("mpris: failed to create album art cache dir",
			log.String("path", dir), log.Error(err))
	}
	purgeArtCacheDir(dir, logger)
	return &ArtCache{
		dir:      dir,
		resolved: make(map[string]string),
	}
}

// purgeArtCacheDir clears the cache when it grows past maxArtCacheFiles.
func purgeArtCacheDir(dir string, logger log.Logger) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	if len(entries) <= maxArtCacheFiles {
		return
	}
	logger.Debug("mpris: clearing oversized album art cache",
		log.Int("files", len(entries)))
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

// Key derives the cache filename stem: the kdeArtHash parameter when present,
// otherwise FNV-1a of the full URI.
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

// Commit bounds and sanitizes art before caching: dimensions gate allocation,
// and the cache holds a fresh JPEG re-encode, never peer bytes with chunks.
func (c *ArtCache) Commit(rawURL, tmpPath string) (string, error) {
	defer os.Remove(tmpPath)

	f, err := os.Open(tmpPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Header only: no raster allocation yet.
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return "", fmt.Errorf("artcache: bad image header: %w", err)
	}
	w, h := int64(cfg.Width), int64(cfg.Height)
	if w <= 0 || h <= 0 || w > maxArtDimension || h > maxArtDimension || w*h > maxArtPixels {
		return "", fmt.Errorf("artcache: dimensions rejected: %dx%d", cfg.Width, cfg.Height)
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	// Full decode is dimension-gated above; Go skips ancillary text chunks
	// rather than retaining them. Re-encoding strips whatever survives for
	// downstream decoders that do retain them.
	img, _, err := image.Decode(f)
	if err != nil {
		return "", fmt.Errorf("artcache: decode failed: %w", err)
	}

	// Written in place inside the cache dir: no cross-device rename involved.
	dst := filepath.Join(c.dir, c.Key(rawURL)+".jpg")
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return "", err
	}
	if err := jpeg.Encode(out, img, &jpeg.Options{Quality: 85}); err != nil {
		out.Close()
		os.Remove(dst)
		return "", fmt.Errorf("artcache: re-encode failed: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return "", fmt.Errorf("artcache: close failed: %w", err)
	}

	fileURL := "file://" + dst
	c.mu.Lock()
	c.resolved[rawURL] = fileURL
	c.mu.Unlock()
	return fileURL, nil
}
