package sftp

import (
	"bufio"
	"os"
	"strings"
)

// mountTablePath reads /proc directly instead of shelling out, so there is
// no external dependency to fail. It is a var so tests can fixture it.
var mountTablePath = "/proc/mounts"

// liveMountPoint asks the kernel, not the map: in-memory state forgets
// live FUSE mounts across restarts, and persisting would go stale on crash.

// mountExists reports whether the kernel holds a FUSE mount here, telling
// "nothing mounted" apart from "release failed" for Unmount's callers.
func mountExists(path string) bool {
	_, found := eachMount(func(mountPoint string) bool { return mountPoint == path })
	return found
}

// eachMount returns the first matching FUSE mount. FUSE-only on purpose:
// adopting an unrelated same-named mount risks fusermount against it.
func eachMount(match func(mountPoint string) bool) (string, bool) {
	f, err := os.Open(mountTablePath)
	if err != nil {
		return "", false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || !strings.HasPrefix(fields[2], "fuse") {
			continue
		}
		mountPoint := unescapeMountPath(fields[1])
		if match(mountPoint) {
			return mountPoint, true
		}
	}
	return "", false
}

// unescapeMountPath reverses the kernel's octal escaping of whitespace in
// mountpoints (Getwd-style: space, tab, newline, backslash only).
func unescapeMountPath(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+3 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		var v byte
		switch {
		case s[i+1] >= '0' && s[i+1] <= '7' && s[i+2] >= '0' && s[i+2] <= '7' && s[i+3] >= '0' && s[i+3] <= '7':
			v = (s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0')
		default:
			b.WriteByte(s[i])
			continue
		}
		b.WriteByte(v)
		i += 3
	}
	return b.String()
}
