package sftp

import (
	"fmt"
	"testing"
)

// remoteRoot constructs the sshfs remote root from an SftpBody.
// Mirrors the logic in mountWithBody.
func remoteRoot(body SftpBody) string {
	remotePath := ""
	if len(body.MultiPaths) > 0 {
		remotePath = body.MultiPaths[0]
	} else if body.Path != "" && body.Path != "/" {
		remotePath = body.Path
	}
	return fmt.Sprintf("%s@%s:%s", body.User, body.IP, remotePath)
}

func TestRemoteRoot_SingleVolume(t *testing.T) {
	body := SftpBody{
		IP:         "192.168.1.42",
		Port:       "1776",
		User:       "u0_a123",
		Password:   "sekret",
		Path:       "/storage/emulated/0",
		MultiPaths: []string{"/storage/emulated/0"},
		PathNames:  []string{"Internal shared storage"},
	}
	want := "u0_a123@192.168.1.42:/storage/emulated/0"
	if got := remoteRoot(body); got != want {
		t.Errorf("remoteRoot = %q, want %q", got, want)
	}
}

func TestRemoteRoot_MultiVolume(t *testing.T) {
	body := SftpBody{
		IP:         "192.168.1.42",
		Port:       "1776",
		User:       "u0_a123",
		Password:   "sekret",
		Path:       "/",
		MultiPaths: []string{"/storage/emulated/0", "/storage/ABCD-1234"},
		PathNames:  []string{"Internal shared storage", "SD card"},
	}
	// Should use first multiPaths entry, not path="/"
	want := "u0_a123@192.168.1.42:/storage/emulated/0"
	if got := remoteRoot(body); got != want {
		t.Errorf("remoteRoot = %q, want %q", got, want)
	}
}

func TestRemoteRoot_FallbackToPath(t *testing.T) {
	body := SftpBody{
		IP:       "192.168.1.42",
		Port:     "1776",
		User:     "u0_a123",
		Password: "sekret",
		Path:     "/storage/emulated/0",
	}
	want := "u0_a123@192.168.1.42:/storage/emulated/0"
	if got := remoteRoot(body); got != want {
		t.Errorf("remoteRoot = %q, want %q", got, want)
	}
}

func TestRemoteRoot_NoPath(t *testing.T) {
	body := SftpBody{
		IP:       "192.168.1.42",
		Port:     "1776",
		User:     "u0_a123",
		Password: "sekret",
	}
	// No path available — fall back to empty (legacy root mount)
	want := "u0_a123@192.168.1.42:"
	if got := remoteRoot(body); got != want {
		t.Errorf("remoteRoot = %q, want %q", got, want)
	}
}

func TestRemoteRoot_PathIsSlash(t *testing.T) {
	body := SftpBody{
		IP:   "192.168.1.42",
		User: "u0_a123",
		Path: "/",
	}
	// Path "/" is a fallback indicator for multi-volume — no MultiPaths means
	// we treat it as absent.
	want := "u0_a123@192.168.1.42:"
	if got := remoteRoot(body); got != want {
		t.Errorf("remoteRoot = %q, want %q", got, want)
	}
}
