package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// setProbe points the plugin's probe at fn for the lifetime of the plugin.
// It is safe to call before any async goroutine spawns (tests set it, then
// never write it again, so the fire-and-forget Handle goroutine only reads).
func setProbe(t *testing.T, p *ClipboardPlugin, fn func() (clipboardBackend, string)) {
	t.Helper()
	p.probe = fn
}

// shimBin puts executable stand-ins for the given tools at the front of PATH,
// so probeBackend does not depend on what's installed on the host.
func shimBin(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestProbeBackend_WaylandFromSocket(t *testing.T) {
	// No WAYLAND_DISPLAY in the env, but a live socket in the runtime dir —
	// the systemd-user-service boot case. Must resolve to Wayland.
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", ":0")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	shimBin(t, "wl-paste")
	sock := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "wayland-1")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	backend, disp := probeBackend()
	if backend != backendWayland {
		t.Fatalf("expected wayland via socket probe, got %v", backend)
	}
	if disp != "wayland-1" {
		t.Fatalf("expected wayland-1, got %q", disp)
	}
}

func TestProbeBackend_WaylandFromEnv(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	shimBin(t, "wl-paste")
	sock := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "wayland-0")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	backend, disp := probeBackend()
	if backend != backendWayland || disp != "wayland-0" {
		t.Fatalf("expected wayland-0, got backend=%v disp=%q", backend, disp)
	}
}

func TestProbeBackend_StaleEnvFallsBackToSocket(t *testing.T) {
	// WAYLAND_DISPLAY points at a socket that no longer exists, but another
	// live socket is present — the probe must not trust the stale var.
	t.Setenv("WAYLAND_DISPLAY", "wayland-99")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	shimBin(t, "wl-paste")
	sock := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "wayland-3")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	backend, disp := probeBackend()
	if backend != backendWayland || disp != "wayland-3" {
		t.Fatalf("expected fallback to wayland-3, got backend=%v disp=%q", backend, disp)
	}
}

func TestProbeBackend_X11Fallback(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", ":0")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	shimBin(t, "xclip")

	backend, disp := probeBackend()
	if backend != backendX11 {
		t.Fatalf("expected x11 fallback, got %v", backend)
	}
	if disp != "" {
		t.Fatalf("expected empty display for x11, got %q", disp)
	}
}

func TestProbeBackend_None(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	shimBin(t, "wl-paste", "xclip")

	backend, _ := probeBackend()
	if backend != backendUnknown {
		t.Fatalf("expected unknown backend, got %v", backend)
	}
}

func TestClipboardPlugin_ReProbesAfterFailure(t *testing.T) {
	p := NewClipboardPlugin(nil, zap.NewNop(), false)

	// First probe fails (compositor not up at boot) — nothing cached.
	setProbe(t, p, func() (clipboardBackend, string) { return backendUnknown, "" })
	if b, _ := p.getBackend(); b != backendUnknown {
		t.Fatalf("expected unknown on first probe, got %v", b)
	}

	// Wayland becomes available later — the next call must re-probe and cache it.
	setProbe(t, p, func() (clipboardBackend, string) { return backendWayland, "wayland-1" })
	if b, d := p.getBackend(); b != backendWayland || d != "wayland-1" {
		t.Fatalf("expected wayland-1 after re-probe, got backend=%v disp=%q", b, d)
	}

	// Now cached — a broken probe afterwards must not clobber it.
	setProbe(t, p, func() (clipboardBackend, string) { return backendUnknown, "" })
	if b, d := p.getBackend(); b != backendWayland || d != "wayland-1" {
		t.Fatalf("expected cached wayland-1, got backend=%v disp=%q", b, d)
	}
}

func TestClipboardPlugin_CmdInjectsWaylandEnv(t *testing.T) {
	p := NewClipboardPlugin(nil, zap.NewNop(), false)
	setProbe(t, p, func() (clipboardBackend, string) { return backendWayland, "wayland-9" })

	cmd := p.clipboardCmd(context.Background(), "wl-copy")
	found := false
	for _, kv := range cmd.Env {
		if kv == "WAYLAND_DISPLAY=wayland-9" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected WAYLAND_DISPLAY=wayland-9 in subprocess env, got %v", cmd.Env)
	}
}

func TestClipboardPlugin_CmdHasTimeout(t *testing.T) {
	p := NewClipboardPlugin(nil, zap.NewNop(), false)
	setProbe(t, p, func() (clipboardBackend, string) { return backendWayland, "wayland-1" })

	// A hung subprocess (sleep 30) must be killed by the 2s context timeout.
	start := time.Now()
	cmd := p.clipboardCmd(context.Background(), "sleep", "30")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected a timeout error from a hung clipboard subprocess")
	}
	if wait := time.Since(start); wait > 3*clipboardTimeout {
		t.Fatalf("expected subprocess killed within ~2s, took %v", wait)
	}
}

func TestClipboardPlugin_Handle(t *testing.T) {
	logger := zap.NewNop()
	dev := device.NewDevice("dev1", "Test", "phone", logger)
	p := NewClipboardPlugin(nil, logger, false)
	// Force the no-backend path so no real clipboard tool is invoked.
	setProbe(t, p, func() (clipboardBackend, string) { return backendUnknown, "" })

	body := ClipboardBody{
		Content: "Hello world!",
	}
	pkt, _ := protocol.NewPacket("kdeconnect.clipboard", body)

	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}
}
