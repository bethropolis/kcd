package sftp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bethropolis/kcd/internal/config"
)

func TestUnescapeMountPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain path is untouched", in: "/mnt/kcd-sftp-dev1", want: "/mnt/kcd-sftp-dev1"},
		{name: "space", in: `/mnt/my\040disk`, want: "/mnt/my disk"},
		{name: "tab", in: `/mnt/a\011b`, want: "/mnt/a\tb"},
		{name: "newline", in: `/mnt/a\012b`, want: "/mnt/a\nb"},
		{name: "backslash", in: `/mnt/a\134b`, want: `/mnt/a\b`},
		{name: "several escapes", in: `/mnt/a\040b\040c`, want: "/mnt/a b c"},
		{name: "trailing lone backslash", in: `/mnt/odd\`, want: `/mnt/odd\`},
		{name: "non-octal escape is left alone", in: `/mnt/a\999b`, want: `/mnt/a\999b`},
		{name: "truncated escape is left alone", in: `/mnt/a\04`, want: `/mnt/a\04`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := unescapeMountPath(tc.in); got != tc.want {
				t.Errorf("unescapeMountPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// useFakeMountTable points the reconciliation at a fixture for one test.
func useFakeMountTable(t *testing.T, contents string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mounts")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	mountTableMu.Lock()
	orig := mountTablePath
	mountTablePath = path
	mountTableMu.Unlock()
	t.Cleanup(func() {
		mountTableMu.Lock()
		mountTablePath = orig
		mountTableMu.Unlock()
	})
}

// A mount the daemon has forgotten but the kernel still has is the
// post-restart state: the FUSE mount survived, the daemon's memory did not.
// It has to be adopted, or unmount reports "not mounted" while the mount
// keeps holding an sshfs process.
func TestMountedPath_AdoptsOrphanFromKernel(t *testing.T) {
	p := newTestPlugin(t, t.TempDir())
	mountPoint := p.mountPointFor("dev1")
	useFakeMountTable(t, "proc /proc proc rw 0 0\nphone:/storage/emulated/0 "+mountPoint+" fuse.sshfs rw 0 0\n")

	if _, cached := p.mountPoints["dev1"]; cached {
		t.Fatal("test precondition: cache should start empty")
	}
	if got := p.MountedPath("dev1"); got != mountPoint {
		t.Fatalf("MountedPath = %q, want %q", got, mountPoint)
	}
	if _, adopted := p.mountPoints["dev1"]; !adopted {
		t.Error("orphan was resolved but not adopted, so every lookup re-reads /proc")
	}
	if !p.IsMounted("dev1") {
		t.Error("IsMounted = false for a live mount the daemon had forgotten")
	}
}

// A mount at the location kcd used to default to has to stay findable, or
// upgrading orphans exactly the mounts the user already has.
func TestMountedPath_AdoptsLegacyLocation(t *testing.T) {
	p := newTestPlugin(t, t.TempDir())
	legacy := legacyMountPointFor("dev1")
	if legacy == "" {
		t.Skip("no home directory in this environment")
	}
	useFakeMountTable(t, "phone:/storage/emulated/0 "+legacy+" fuse.sshfs rw 0 0\n")

	if got := p.MountedPath("dev1"); got != legacy {
		t.Fatalf("MountedPath = %q, want the legacy mount at %q", got, legacy)
	}
	if cached := p.mountPoints["dev1"]; cached != legacy {
		t.Errorf("cache = %q, want %q", cached, legacy)
	}
}

// The configured location wins when both exist, so the stale one is left
// visible to the user rather than silently adopted.
func TestMountedPath_PrefersConfiguredOverLegacy(t *testing.T) {
	p := newTestPlugin(t, t.TempDir())
	primary := p.mountPointFor("dev1")
	legacy := legacyMountPointFor("dev1")
	if legacy == "" || legacy == primary {
		t.Skip("no distinct legacy path in this environment")
	}
	useFakeMountTable(t, "a "+legacy+" fuse.sshfs rw 0 0\nb "+primary+" fuse.sshfs rw 0 0\n")

	if got := p.MountedPath("dev1"); got != primary {
		t.Errorf("MountedPath = %q, want the configured location %q", got, primary)
	}
}

func TestMountedPath_DoesNotAdoptAnotherDevice(t *testing.T) {
	p := newTestPlugin(t, t.TempDir())
	useFakeMountTable(t, "phone:/storage/emulated/0 "+p.mountPointFor("other")+" fuse.sshfs rw 0 0\n")

	if got := p.MountedPath("dev1"); got != "" {
		t.Errorf("adopted another device's mount: %q", got)
	}
}

// Non-FUSE mounts sharing the naming scheme must be left alone: adopting one
// and later running fusermount against it would be worse than not adopting.
func TestMountedPath_IgnoresNonFuseEntries(t *testing.T) {
	p := newTestPlugin(t, t.TempDir())
	useFakeMountTable(t, "/dev/sdb1 "+p.mountPointFor("dev1")+" ext4 rw 0 0\n")

	if got := p.MountedPath("dev1"); got != "" {
		t.Errorf("adopted a non-FUSE mount: %q", got)
	}
}

// Mount points containing spaces arrive octal-escaped from the kernel.
func TestMountedPath_UnescapesKernelPath(t *testing.T) {
	// t.TempDir() has no space in it, so build the mount dir explicitly.
	p := newTestPlugin(t, filepath.Join(t.TempDir(), "my mount dir"))
	want := p.mountPointFor("dev1")
	if !strings.Contains(want, " ") {
		t.Fatal("test setup: mount point should contain a space")
	}
	escaped := strings.ReplaceAll(want, " ", `\040`)
	useFakeMountTable(t, "phone:/storage/ABCD "+escaped+" fuse.sshfs rw 0 0\n")

	if got := p.MountedPath("dev1"); got != want {
		t.Errorf("MountedPath = %q, want the unescaped %q", got, want)
	}
}

// An absent mount reports not mounted and stays out of the cache.
func TestMountedPath_AbsentLeavesCacheClean(t *testing.T) {
	useFakeMountTable(t, "proc /proc proc rw 0 0\n")

	p := newTestPlugin(t, t.TempDir())
	if got := p.MountedPath("dev1"); got != "" {
		t.Errorf("MountedPath = %q, want empty", got)
	}
	if _, cached := p.mountPoints["dev1"]; cached {
		t.Error("cached an empty resolution")
	}
}

// The default must not be reachable by a routine `rm -rf` of a user
// directory. This is the safety property the whole move is about.
func TestDefaultMountDir_IsNotUnderUserDocuments(t *testing.T) {
	dir := config.DefaultMountDir()
	for _, doc := range []string{"Downloads", "Documents", "Desktop"} {
		if strings.Contains(dir, string(filepath.Separator)+doc+string(filepath.Separator)) ||
			strings.HasSuffix(dir, string(filepath.Separator)+doc) {
			t.Errorf("default mount dir %q sits inside a user document directory", dir)
		}
	}
}
