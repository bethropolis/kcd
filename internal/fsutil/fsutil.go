package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxFilenameBytes is the maximum allowed byte length for a filename, matching
// standard POSIX NAME_MAX.
const MaxFilenameBytes = 255

// DefaultFallbackFilename is used when a filename becomes empty after sanitization.
const DefaultFallbackFilename = "downloaded_file"

// SanitizeFilename hardens untrusted filenames: normalizes backslashes, strips
// directory traversal, drops control characters and leading dots, bounds byte
// length to 255 bytes on a rune boundary, and returns a fallback if empty.
func SanitizeFilename(name string, fallback ...string) string {
	fb := DefaultFallbackFilename
	if len(fallback) > 0 && fallback[0] != "" {
		fb = fallback[0]
	}

	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' || r == 0 {
			return -1
		}
		return r
	}, name)

	name = strings.TrimLeft(name, ".")
	name = strings.TrimSpace(name)

	if len(name) > MaxFilenameBytes {
		// Truncate at or before 255 bytes on a valid rune boundary.
		boundary := MaxFilenameBytes
		for boundary > 0 && !utf8.RuneStart(name[boundary]) {
			boundary--
		}
		name = name[:boundary]
		name = strings.ToValidUTF8(name, "")
	}

	if name == "" || name == "." || name == ".." {
		return fb
	}
	return name
}

// CreateUnique opens a new unique file in dir using O_CREATE|O_EXCL to prevent
// race conditions and overwriting between concurrent transfers.
func CreateUnique(dir, name string) (*os.File, string, error) {
	name = SanitizeFilename(name)
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)

	for counter := 0; counter < 10000; counter++ {
		candidate := name
		if counter > 0 {
			candidate = fmt.Sprintf("%s_%d%s", base, counter, ext)
		}
		fullPath := filepath.Join(dir, candidate)
		f, err := os.OpenFile(fullPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			return f, fullPath, nil
		}
		if os.IsExist(err) {
			continue
		}
		return nil, "", fmt.Errorf("fsutil: create unique file: %w", err)
	}

	return nil, "", fmt.Errorf("fsutil: exhausted unique filename attempts in %s", dir)
}

// EnsureUnique returns an available unique path in dir, creating an exclusive
// placeholder to eliminate race conditions between concurrent transfers.
func EnsureUnique(dir, name string) (string, error) {
	f, path, err := CreateUnique(dir, name)
	if err != nil {
		return "", err
	}
	_ = f.Close()
	return path, nil
}
