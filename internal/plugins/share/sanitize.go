package share

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/bethropolis/kcd/internal/fsutil"
)

// SanitizeFilename strips path components and prevents traversal.
func SanitizeFilename(name string) string {
	return fsutil.SanitizeFilename(name)
}

// isOpenableURL reports whether a phone-shared URL may reach xdg-open: only
// http(s) with a host, since schemes dispatch to unrelated local programs.
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

// blockedAutoOpenExts are file types never auto-opened after download:
// handing them to the desktop handler can execute code (.desktop files).
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
	return fsutil.EnsureUnique(dir, name)
}
