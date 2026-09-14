package share

import (
	"fmt"
	"net/url"
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

// isOpenableURL reports whether a phone-shared URL may be handed to
// xdg-open. Only http(s) with a host qualifies: other schemes (file://,
// smb:, mailto:, custom app handlers) would dispatch phone-influenced input
// to unrelated local programs.
func isOpenableURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}

// blockedAutoOpenExts are file types that must never be auto-opened after
// download. Handing them to the desktop handler can execute code (notably
// .desktop files, which most environments treat as launchers).
var blockedAutoOpenExts = map[string]struct{}{
	".desktop": {}, ".sh": {}, ".bin": {}, ".run": {}, ".jar": {},
	".py": {}, ".pl": {}, ".rb": {}, ".php": {}, ".js": {},
	".exe": {}, ".msi": {}, ".bat": {}, ".cmd": {}, ".com": {},
	".scr": {}, ".ps1": {}, ".vbs": {}, ".vbe": {}, ".jse": {},
	".wsf": {}, ".wsh": {}, ".hta": {}, ".lnk": {}, ".gadget": {},
}

// autoOpenBlocked reports whether a downloaded file must be excluded from
// auto-open because of its extension.
func autoOpenBlocked(path string) bool {
	_, blocked := blockedAutoOpenExts[strings.ToLower(filepath.Ext(path))]
	return blocked
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
