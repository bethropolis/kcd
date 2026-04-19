package share

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SanitizeFilename strips path components and prevents traversal.
func SanitizeFilename(name string) string {
	// 1. Normalize separators to handle cross-platform paths (e.g., Windows \ vs Linux /)
	name = strings.ReplaceAll(name, "\\", "/")

	// 2. Get base name to strip directory paths
	name = filepath.Base(name)

	// 3. Explicitly remove any common path traversal or separators
	name = strings.ReplaceAll(name, "/", "")
	name = strings.ReplaceAll(name, "\\", "")
	name = strings.ReplaceAll(name, "..", "")

	// 3. Prevent empty or dot-only filenames
	if name == "." || name == ".." || name == "" {
		return "downloaded_file"
	}

	return name
}

// EnsureUnique finds a non-conflicting filename in the destination directory.
func EnsureUnique(dir, name string) (string, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)

	proposed := filepath.Join(dir, name)
	counter := 1

	for {
		if _, err := os.Stat(proposed); os.IsNotExist(err) {
			return proposed, nil
		} else if err != nil {
			return "", fmt.Errorf("share: check file exists: %w", err)
		}

		// Try appending a counter
		name = fmt.Sprintf("%s_%d%s", base, counter, ext)
		proposed = filepath.Join(dir, name)
		counter++

		if counter > 1000 {
			return "", fmt.Errorf("share: too many filename collisions for %s", name)
		}
	}
}
