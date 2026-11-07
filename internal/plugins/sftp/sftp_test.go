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

func validBody() SftpBody {
	return SftpBody{
		IP:       "192.168.1.42",
		Port:     "1776",
		User:     "u0_a123",
		Password: "sekret",
		Path:     "/storage/emulated/0",
	}
}

func TestBuildSSHFSArgs_Valid(t *testing.T) {
	args, err := buildSSHFSArgs(validBody(), "/storage/emulated/0", "/mnt/kcd", 1000, 1000, 15, 3, nil, false)
	if err != nil {
		t.Fatalf("buildSSHFSArgs returned error: %v", err)
	}
	if len(args) < 3 || args[0] != "u0_a123@192.168.1.42:/storage/emulated/0" || args[1] != "/mnt/kcd" {
		t.Fatalf("unexpected remote/mount args: %q", args)
	}
	for i, a := range args {
		if i > 1 && a == "1776" {
			break
		}
		if i == len(args)-1 {
			t.Errorf("validated port missing from args: %q", args)
		}
	}
	for _, a := range args {
		if a == "-oProxyCommand=x" {
			t.Errorf("injected option leaked into args: %q", args)
		}
	}
}

func TestBuildSSHFSArgs_RejectsInjection(t *testing.T) {
	cases := []struct {
		name string
		body SftpBody
		path string
	}{
		{"user flag", SftpBody{IP: "192.168.1.42", Port: "22", User: "-oProxyCommand=evil", Path: "/x"}, "/x"},
		{"user lone dash", SftpBody{IP: "192.168.1.42", Port: "22", User: "-", Path: "/x"}, "/x"},
		{"empty user", SftpBody{IP: "192.168.1.42", Port: "22", Path: "/x"}, "/x"},
		{"host flag", SftpBody{IP: "-oFoo", Port: "22", User: "u", Path: "/x"}, "/x"},
		{"host spaces", SftpBody{IP: "a b", Port: "22", User: "u", Path: "/x"}, "/x"},
		{"host at", SftpBody{IP: "a@b", Port: "22", User: "u", Path: "/x"}, "/x"},
		{"port zero", SftpBody{IP: "192.168.1.42", Port: "0", User: "u", Path: "/x"}, "/x"},
		{"port huge", SftpBody{IP: "192.168.1.42", Port: "99999", User: "u", Path: "/x"}, "/x"},
		{"port alpha", SftpBody{IP: "192.168.1.42", Port: "abc", User: "u", Path: "/x"}, "/x"},
		{"path flag", SftpBody{IP: "192.168.1.42", Port: "22", User: "u", Path: "/x"}, "-oFoo"},
		{"empty path", SftpBody{IP: "192.168.1.42", Port: "22", User: "u", Path: "/x"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildSSHFSArgs(tc.body, tc.path, "/mnt/kcd", 1000, 1000, 15, 3, nil, false); err == nil {
				t.Errorf("buildSSHFSArgs accepted %+v / %q, want error", tc.body, tc.path)
			}
		})
	}
}

func TestBuildSSHFSArgs_Hostnames(t *testing.T) {
	for _, host := range []string{"phone.local", "android-1", "192.168.1.42", "::1"} {
		body := validBody()
		body.IP = host
		if _, err := buildSSHFSArgs(body, "/x", "/mnt/kcd", 1000, 1000, 15, 3, nil, false); err != nil {
			t.Errorf("buildSSHFSArgs(%q) = %v, want nil", host, err)
		}
	}
}

// -o ro is what makes deletion impossible: the kernel rejects unlink/rmdir
// before it ever reaches the phone, so an accidental `rm` cannot propagate.
func TestBuildSSHFSArgs_ReadOnly(t *testing.T) {
	args, err := buildSSHFSArgs(validBody(), "/storage/emulated/0", "/mnt/kcd", 1000, 1000, 15, 3, nil, true)
	if err != nil {
		t.Fatalf("buildSSHFSArgs: %v", err)
	}
	if !containsPair(args, "ro") {
		t.Errorf("expected -o ro in %v", args)
	}

	args, err = buildSSHFSArgs(validBody(), "/storage/emulated/0", "/mnt/kcd", 1000, 1000, 15, 3, nil, false)
	if err != nil {
		t.Fatalf("buildSSHFSArgs: %v", err)
	}
	if containsPair(args, "ro") {
		t.Errorf("did not ask for read-only but got -o ro in %v", args)
	}
}

// ExtraSshfsOpts is operator config, so it has to be able to override the
// per-request mode -- hence -o ro being emitted before it.
func TestBuildSSHFSArgs_ExtraOptsCanOverrideReadOnly(t *testing.T) {
	args, err := buildSSHFSArgs(validBody(), "/storage/emulated/0", "/mnt/kcd", 1000, 1000, 15, 3, []string{"rw"}, true)
	if err != nil {
		t.Fatalf("buildSSHFSArgs: %v", err)
	}
	// sshfs takes the last -o ro/rw, so the operator's "rw" must come after ours.
	roIdx, rwIdx := indexOfPair(args, "ro"), indexOfPair(args, "rw")
	if roIdx < 0 || rwIdx < 0 {
		t.Fatalf("expected both ro and rw in %v", args)
	}
	if roIdx > rwIdx {
		t.Errorf("operator override must follow -o ro, got ro at %d and rw at %d in %v", roIdx, rwIdx, args)
	}
}

func TestReadOnlyByDefault(t *testing.T) {
	p := newTestPlugin(t, t.TempDir())
	if p.ReadOnlyByDefault() {
		t.Error("default should be writable; read-only is opt-in")
	}
	p.cfg.ReadOnly = true
	if !p.ReadOnlyByDefault() {
		t.Error("config default not reported")
	}
}

// The mode an existing mount was created with has to be remembered, since a
// later --ro cannot change it and the log must say what the mount actually is.
func TestMountIsReadOnly_TracksCreatedMode(t *testing.T) {
	p := newTestPlugin(t, t.TempDir())
	if p.mountIsReadOnly("dev1") {
		t.Error("an untracked device must not report read-only")
	}
	p.mountReadOnly["dev1"] = true
	if !p.mountIsReadOnly("dev1") {
		t.Error("recorded mode not reported")
	}
}

func containsPair(args []string, opt string) bool { return indexOfPair(args, opt) >= 0 }

func indexOfPair(args []string, opt string) int {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-o" && args[i+1] == opt {
			return i
		}
	}
	return -1
}
