package sftp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
)

func newTestPlugin(t *testing.T, mountDir string) *SftpPlugin {
	t.Helper()
	cfg := config.SFTPConfig{}
	cfg.MountDir = mountDir
	return NewSftpPlugin(cfg, nil, log.NewTest(t))
}

// Mounting an already-mounted device must not reach sshfs. Verified without a
// real sshfs by pointing an existing mount entry at the path: a second mount
// returns that path instead of trying to exec over it.
func TestMountWithBody_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	p := newTestPlugin(t, dir)

	existing := filepath.Join(dir, "kcd-sftp-dev1")
	if err := os.MkdirAll(existing, 0700); err != nil {
		t.Fatal(err)
	}
	useFakeMountTable(t, "phone:/storage/emulated/0 "+existing+" fuse.sshfs rw 0 0\n")
	p.mountPoints["dev1"] = existing

	body := SftpBody{IP: "192.168.1.42", Port: "1776", User: "u0_a123", Password: "x", Path: "/storage/emulated/0"}

	got, err := p.mountWithBody(context.Background(), "dev1", body, "", false)
	if err != nil {
		t.Fatalf("second mount should succeed by reusing the mount point, got: %v", err)
	}
	if got != existing {
		t.Errorf("second mount returned %q, want the existing mount point %q", got, existing)
	}
}

func TestUnmount_NotMountedIsDistinguishable(t *testing.T) {
	p := newTestPlugin(t, t.TempDir())

	err := p.Unmount("never-mounted")
	if err == nil {
		t.Fatal("expected an error for an unmounted device")
	}
	if !strings.Contains(err.Error(), "not mounted") {
		t.Errorf("error should say 'not mounted' so clients can react to it, got: %v", err)
	}
}

func TestSshfsHint(t *testing.T) {
	tests := []struct {
		name    string
		msg     string
		wantSub string // "" means no hint expected
	}{
		{
			name:    "fuse permission denied",
			msg:     "fusermount3: failed to access mountpoint /mnt/x: Permission denied",
			wantSub: "/etc/fuse.conf",
		},
		{
			name:    "operation not permitted",
			msg:     "fusermount: mount failed: Operation not permitted",
			wantSub: "/etc/fuse.conf",
		},
		{
			name:    "sshfs binary missing",
			msg:     `exec: "sshfs": executable file not found in $PATH`,
			wantSub: "sshfs is not installed",
		},
		{
			// Textually indistinguishable from the fuse.conf case above, and
			// genuinely ambiguous on its own. mountWithBody is now idempotent,
			// so this message is no longer produced by a duplicate mount --
			// the ambiguity is resolved upstream rather than by the hint.
			name:    "permission denied on mountpoint",
			msg:     "fusermount3: failed to access mountpoint /mnt/kcd-sftp-dev1: Permission denied",
			wantSub: "/etc/fuse.conf",
		},
		{
			// Names fusermount3 but is not a fuse.conf problem: this is the
			// class of error the old substring match mis-advised on.
			name:    "unrelated fusermount error",
			msg:     "fusermount3: entry for /mnt/kcd-sftp-dev1 is not a directory",
			wantSub: "",
		},
		{
			name:    "unknown failure",
			msg:     "read: Connection reset by peer",
			wantSub: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sshfsHint(tc.msg)
			if tc.wantSub == "" {
				if got != "" {
					t.Errorf("expected no hint, got:\n%s", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("hint missing %q, got:\n%s", tc.wantSub, got)
			}
		})
	}
}

// Mount state transitions must reach the bus, since that is the only way a
// client can render a mount toggle without inspecting the host mount table.
func TestMountStateEventsPublished(t *testing.T) {
	bus := events.NewBus(log.NewTest(t))
	sub := bus.Subscribe(0, events.TypeSftpMounted, events.TypeSftpUnmounted)
	defer sub.Close()

	p := newTestPlugin(t, t.TempDir())
	p.bus = bus

	// The idempotent path reuses the mount point without a state change, so it
	// must not announce a transition that did not happen.
	mountPoint := "/mnt/kcd-sftp-dev1"
	useFakeMountTable(t, "phone:/storage/emulated/0 "+mountPoint+" fuse.sshfs rw 0 0\n")
	p.mountPoints["dev1"] = mountPoint
	if _, err := p.mountWithBody(context.Background(), "dev1", SftpBody{}, "", false); err != nil {
		t.Fatalf("idempotent mount: %v", err)
	}
	select {
	case ev := <-sub.C:
		t.Fatalf("unexpected event on a no-op mount: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	p.publishMounted("dev1", "/mnt/kcd-sftp-dev1", "/storage/ABCD-1234")
	ev := <-sub.C
	if ev.Type != events.TypeSftpMounted {
		t.Fatalf("got %s, want %s", ev.Type, events.TypeSftpMounted)
	}
	if got := ev.Payload.(map[string]any)["mountPoint"]; got != "/mnt/kcd-sftp-dev1" {
		t.Errorf("mountPoint = %v, want /mnt/kcd-sftp-dev1", got)
	}
	if got := ev.Payload.(map[string]any)["volume"]; got != "/storage/ABCD-1234" {
		t.Errorf("volume = %v, want /storage/ABCD-1234", got)
	}

	p.publishUnmounted("dev1", "/mnt/kcd-sftp-dev1")
	ev = <-sub.C
	if ev.Type != events.TypeSftpUnmounted {
		t.Fatalf("got %s, want %s", ev.Type, events.TypeSftpUnmounted)
	}
}

func TestInfoReportsMountState(t *testing.T) {
	mountPoint := "/mnt/kcd-sftp-dev1"
	useFakeMountTable(t, "phone:/storage/emulated/0 "+mountPoint+" fuse.sshfs rw 0 0\n")
	p := newTestPlugin(t, t.TempDir())
	p.lastBody["dev1"] = SftpBody{IP: "192.168.1.42", User: "u0_a123", Path: "/storage/emulated/0"}

	if info := p.Info("dev1", false); info.Mounted {
		t.Error("reported mounted with no mount tracked")
	}

	p.mountPoints["dev1"] = mountPoint
	info := p.Info("dev1", false)
	if !info.Mounted {
		t.Error("reported not mounted while a mount is tracked")
	}
	if info.MountPoint != mountPoint {
		t.Errorf("MountPoint = %q, want %s", info.MountPoint, mountPoint)
	}
}

// The reported bug: the daemon still had a device cached as mounted, but the
// kernel had no such mount, so fusermount failed with "not found in
// /etc/mtab" forever. Keeping the cached state on failure -- correct for a
// mount that really is still there -- meant this state could never clear,
// leaving the device unable to mount or unmount again.
func TestUnmount_ClearsStateWhenKernelHasNoMount(t *testing.T) {
	dir := t.TempDir()
	useFakeMountTable(t, "proc /proc proc rw 0 0\n")

	p := newTestPlugin(t, dir)
	mountPoint := filepath.Join(dir, "kcd-sftp-dev1")
	if err := os.MkdirAll(mountPoint, 0700); err != nil {
		t.Fatal(err)
	}
	p.mountPoints["dev1"] = mountPoint

	if err := p.Unmount("dev1"); err != nil {
		t.Fatalf("unmounting a mount the kernel already dropped must succeed, got: %v", err)
	}
	if _, cached := p.mountPoints["dev1"]; cached {
		t.Error("cached state survived; the device stays wedged as mounted")
	}
	if _, err := os.Stat(mountPoint); !os.IsNotExist(err) {
		t.Error("leftover mount directory was not removed")
	}
	if p.MountedPath("dev1") != "" {
		t.Error("MountedPath still reports the device as mounted")
	}
}

// With an empty cache and nothing in the kernel, a leftover directory from an
// earlier release should still be cleaned up.
func TestUnmount_RemovesLeftoverDirectoryWithoutCache(t *testing.T) {
	dir := t.TempDir()
	useFakeMountTable(t, "proc /proc proc rw 0 0\n")

	p := newTestPlugin(t, dir)
	leftover := filepath.Join(dir, "kcd-sftp-dev1")
	if err := os.MkdirAll(leftover, 0700); err != nil {
		t.Fatal(err)
	}

	err := p.Unmount("dev1")
	if err == nil || !strings.Contains(err.Error(), "not mounted") {
		t.Errorf("want a 'not mounted' error, got: %v", err)
	}
	if _, statErr := os.Stat(leftover); !os.IsNotExist(statErr) {
		t.Error("leftover directory survived; it will block the next mount's MkdirAll forever")
	}
}

// A genuinely live mount must still be released through fusermount, and the
// normal path must not be short-circuited by the new kernel check.
func TestUnmount_LiveMountTakesTheReleasePath(t *testing.T) {
	dir := t.TempDir()
	mountPoint := filepath.Join(dir, "kcd-sftp-dev1")
	if err := os.MkdirAll(mountPoint, 0700); err != nil {
		t.Fatal(err)
	}

	// Present in the kernel's view, so the release path runs. Nothing is
	// actually mounted here, so fusermount will fail -- the point is that we
	// got as far as trying it and then kept the state, because the mount is
	// (as far as we can tell) still there.
	useFakeMountTable(t, "phone:/storage/emulated/0 "+mountPoint+" fuse.sshfs rw 0 0\n")

	p := newTestPlugin(t, dir)
	p.mountPoints["dev1"] = mountPoint

	err := p.Unmount("dev1")
	if err != nil && !strings.Contains(err.Error(), "could not be released") {
		t.Errorf("unexpected error shape: %v", err)
	}
	if _, cached := p.mountPoints["dev1"]; !cached {
		t.Error("state was dropped for a mount the kernel still reports; it should stay retryable")
	}
}

// Shutdown has to release mounts: OnDisconnect never fires on a graceful stop,
// so without this every restart leaves the phone's storage mounted.
//
// What UnmountAll owns is visiting every tracked device, concurrently, and
// publishing the transition. Releasing a genuinely live mount needs a real
// FUSE mount, which a unit test cannot create -- so the table here reports the
// mounts as already gone, and each Unmount clears its own state on that path.
// The live-release path is covered by TestUnmount_LiveMountTakesTheReleasePath.
func TestUnmountAll_VisitsEveryTrackedDevice(t *testing.T) {
	useFakeMountTable(t, "proc /proc proc rw 0 0\n")

	bus := events.NewBus(log.NewTest(t))
	sub := bus.Subscribe(0, events.TypeSftpUnmounted)
	defer sub.Close()

	p := newTestPlugin(t, t.TempDir())
	p.bus = bus
	for _, id := range []string{"dev1", "dev2", "dev3"} {
		p.mountPoints[id] = p.mountPointFor(id)
	}

	p.UnmountAll(context.Background())

	if left := p.mountPoints; len(left) != 0 {
		t.Errorf("still tracking %d mount(s) after UnmountAll: %v", len(left), left)
	}
	for i := range 3 {
		select {
		case <-sub.C:
		case <-time.After(time.Second):
			t.Fatalf("only %d of 3 sftp.unmounted events arrived", i)
		}
	}
}

func TestUnmountAll_NoMountsIsANoOp(t *testing.T) {
	p := newTestPlugin(t, t.TempDir())
	p.UnmountAll(context.Background()) // must not block or panic
}

// The budget is what protects shutdown from a wedged FUSE mount, so an expired
// context has to end the wait rather than hang it.
func TestUnmountAll_RespectsContextBudget(t *testing.T) {
	p := newTestPlugin(t, t.TempDir())
	p.mountPoints["dev1"] = p.mountPointFor("dev1")
	// The kernel does not know this mount, so Unmount takes the
	// already-released path and returns; the point is that a cancelled context
	// still terminates the call.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		p.UnmountAll(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("UnmountAll ignored a cancelled context")
	}

	// UnmountAll returns as soon as ctx is done, so the unmount it spawned is
	// still in flight. Wait for it before the test ends: it logs as it goes,
	// and t.Log after the test completes is what the race detector catches.
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.mu.RLock()
		_, tracked := p.mountPoints["dev1"]
		p.mu.RUnlock()
		if !tracked {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the in-flight unmount never finished after UnmountAll returned")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A cached mount dropped by the kernel must be evicted by MountedPath,
// clearing memory, publishing sftp.unmounted, and deleting the leftover directory.
func TestMountedPath_ClearsStaleCacheWhenKernelHasNoMount(t *testing.T) {
	dir := t.TempDir()
	useFakeMountTable(t, "proc /proc proc rw 0 0\n")

	bus := events.NewBus(log.NewTest(t))
	sub := bus.Subscribe(0, events.TypeSftpUnmounted)
	defer sub.Close()

	p := newTestPlugin(t, dir)
	p.bus = bus
	mountPoint := filepath.Join(dir, "kcd-sftp-dev1")
	if err := os.MkdirAll(mountPoint, 0700); err != nil {
		t.Fatal(err)
	}
	p.mountPoints["dev1"] = mountPoint

	if got := p.MountedPath("dev1"); got != "" {
		t.Fatalf("MountedPath should return empty for dropped mount, got %q", got)
	}

	if _, cached := p.mountPoints["dev1"]; cached {
		t.Error("stale cache entry was not evicted")
	}

	if _, err := os.Stat(mountPoint); !os.IsNotExist(err) {
		t.Error("leftover mount directory was not removed")
	}

	select {
	case ev := <-sub.C:
		if ev.Type != events.TypeSftpUnmounted {
			t.Errorf("got event %s, want %s", ev.Type, events.TypeSftpUnmounted)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for sftp.unmounted event")
	}
}

// When a cached mount has dropped from the kernel, mountWithBody must not
// short-circuit with "reusing mount point"; it must clear the dead entry and
// attempt a fresh mount.
func TestMountWithBody_DoesNotReuseStaleMount(t *testing.T) {
	dir := t.TempDir()
	useFakeMountTable(t, "proc /proc proc rw 0 0\n")

	p := newTestPlugin(t, dir)
	mountPoint := filepath.Join(dir, "kcd-sftp-dev1")
	if err := os.MkdirAll(mountPoint, 0700); err != nil {
		t.Fatal(err)
	}
	p.mountPoints["dev1"] = mountPoint

	called := false
	p.runSSHFS = func(ctx context.Context, args []string, password, mp, devID string) error {
		called = true
		if mp != mountPoint {
			t.Errorf("mountPoint = %q, want %q", mp, mountPoint)
		}
		// Simulate successful mount by populating fake mount table.
		useFakeMountTable(t, "phone:/storage/emulated/0 "+mp+" fuse.sshfs rw 0 0\n")
		return nil
	}

	body := SftpBody{IP: "192.168.1.42", Port: "1776", User: "u0_a123", Password: "x", Path: "/storage/emulated/0"}
	got, err := p.mountWithBody(context.Background(), "dev1", body, "", false)
	if err != nil {
		t.Fatalf("mountWithBody failed: %v", err)
	}
	if !called {
		t.Error("mountWithBody incorrectly reused stale mount point without invoking runner")
	}
	if got != mountPoint {
		t.Errorf("got %q, want %q", got, mountPoint)
	}
}

// A failing sshfs run must clean up the mount directory and not retain the cache.
func TestMountWithBody_RunSSHFSFailureCleansUpAndReturnsError(t *testing.T) {
	dir := t.TempDir()
	useFakeMountTable(t, "proc /proc proc rw 0 0\n")

	p := newTestPlugin(t, dir)
	p.runSSHFS = func(ctx context.Context, args []string, password, mp, devID string) error {
		return fmt.Errorf("sshfs failed: connection refused\nread: Connection reset by peer")
	}

	body := SftpBody{IP: "192.168.1.42", Port: "1776", User: "u0_a123", Password: "x", Path: "/storage/emulated/0"}
	_, err := p.mountWithBody(context.Background(), "dev1", body, "", false)
	if err == nil {
		t.Fatal("expected mountWithBody to fail")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("unexpected error: %v", err)
	}

	mountPoint := p.mountPointFor("dev1")
	if _, err := os.Stat(mountPoint); !os.IsNotExist(err) {
		t.Error("mount point directory was not cleaned up on failure")
	}
	if _, cached := p.mountPoints["dev1"]; cached {
		t.Error("failed mount was retained in cache")
	}
}

// Running defaultRunSSHFS with a missing binary returns a clear error with install hint.
func TestDefaultRunSSHFS_MissingBinaryReturnsHint(t *testing.T) {
	// Hermetic: resolve sshfs from an empty dir so a host install cannot
	// turn the missing-binary case into a real mount attempt.
	t.Setenv("PATH", t.TempDir())

	p := newTestPlugin(t, t.TempDir())
	mountPoint := filepath.Join(t.TempDir(), "kcd-sftp-dev1")

	err := p.defaultRunSSHFS(context.Background(), []string{"arg"}, "secret", mountPoint, "dev1")
	if err == nil {
		t.Fatal("expected error from defaultRunSSHFS")
	}
	if !strings.Contains(err.Error(), "sshfs failed:") {
		t.Errorf("expected 'sshfs failed:' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Hint: sshfs is not installed") {
		t.Errorf("expected install hint in error, got: %v", err)
	}
}
