package clipboard

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	cmd := p.clipboardCmd("wl-copy")
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

func TestRunClipboard_Success(t *testing.T) {
	p := NewClipboardPlugin(nil, zap.NewNop(), false)

	out, err := p.runClipboard(context.Background(), exec.CommandContext(context.Background(), "/bin/sh", "-c", "printf hello"))
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if string(out) != "hello" {
		t.Fatalf("expected 'hello', got %q", out)
	}
}

func TestRunClipboard_WrapsStderr(t *testing.T) {
	p := NewClipboardPlugin(nil, zap.NewNop(), false)

	_, err := p.runClipboard(context.Background(), exec.CommandContext(context.Background(), "/bin/sh", "-c", "echo boom >&2; exit 2"))
	if err == nil {
		t.Fatal("expected an error")
	}
	// The real stderr reason must surface instead of a bare "exit status 2".
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected stderr in error, got %v", err)
	}
}

func TestRunClipboard_TimesOutHungSubprocess(t *testing.T) {
	p := NewClipboardPlugin(nil, zap.NewNop(), false)

	// A hung subprocess must be killed by the 2s timeout — and not instantly
	// (that would regress to the old premature-cancel bug).
	start := time.Now()
	_, err := p.runClipboard(context.Background(), exec.CommandContext(context.Background(), "/bin/sh", "-c", "sleep 30"))
	if err == nil {
		t.Fatal("expected a timeout error from a hung clipboard subprocess")
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Fatalf("subprocess killed too early (instant cancel regression?), elapsed=%v", elapsed)
	} else if elapsed > 3*clipboardTimeout {
		t.Fatalf("expected subprocess killed within ~2s, took %v", elapsed)
	}
}

func TestIsNoSelection(t *testing.T) {
	for _, msg := range []string{
		"exit status 1: wl-paste: No selection",
		"exit status 1: Nothing is copied",
		"exit status 1: Error: There is no data to be read",
	} {
		if !isNoSelection(fmt.Errorf("%s", msg)) {
			t.Errorf("expected %q to be treated as no-selection", msg)
		}
	}
	if isNoSelection(fmt.Errorf("exit status 1: failed to connect to compositor")) {
		t.Error("compositor errors must remain hard failures")
	}
}

// fakeSender records packets like a device without touching the network.
type fakeSender struct {
	sent []*protocol.Packet
}

func (f *fakeSender) ID() string                 { return "fake" }
func (f *fakeSender) Name() string               { return "fake" }
func (f *fakeSender) SetName(string)             {}
func (f *fakeSender) State() device.PairingState { return device.StatePaired }
func (f *fakeSender) SetState(device.PairingState) {
}

func (f *fakeSender) Send(p *protocol.Packet) error {
	f.sent = append(f.sent, p)
	return nil
}
func (f *fakeSender) IsConnected() bool           { return true }
func (f *fakeSender) RemoteIP() net.IP            { return net.ParseIP("127.0.0.1") }
func (f *fakeSender) PeerCert() *x509.Certificate { return nil }
func (f *fakeSender) HasCapability(string) bool   { return false }
func (f *fakeSender) UpdateBattery(int, bool)     {}
func (f *fakeSender) GetBattery() (int, bool)     { return 0, false }

// shimWith replaces PATH with a single scripted binary so Push exercises the
// real command execution without depending on the host's clipboard tools.
func shimWith(t *testing.T, name, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestPush_EmptyClipboardIsSoftFail(t *testing.T) {
	p := NewClipboardPlugin(nil, zap.NewNop(), false)
	setProbe(t, p, func() (clipboardBackend, string) { return backendWayland, "wayland-1" })

	// Empty clipboard with nothing copied: wl-paste exits non-zero with a
	// "no selection" diagnostic — push must not fail the CLI for this.
	shimWith(t, "wl-paste", "#!/bin/sh\necho 'No selection' >&2\nexit 1\n")
	dev := &fakeSender{}

	if err := Push(context.Background(), dev, p); err != nil {
		t.Fatalf("expected soft pass on empty clipboard, got %v", err)
	}
	if len(dev.sent) != 0 {
		t.Fatalf("expected no packet for empty clipboard, got %d", len(dev.sent))
	}
}

func TestPush_EmptyOutputSendsNothing(t *testing.T) {
	p := NewClipboardPlugin(nil, zap.NewNop(), false)
	setProbe(t, p, func() (clipboardBackend, string) { return backendWayland, "wayland-1" })

	// Tool reports success but no content — still nothing to push.
	shimWith(t, "wl-paste", "#!/bin/sh\nexit 0\n")
	dev := &fakeSender{}

	if err := Push(context.Background(), dev, p); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(dev.sent) != 0 {
		t.Fatalf("expected no packet for empty output, got %d", len(dev.sent))
	}
}

func TestPush_SendsContent(t *testing.T) {
	p := NewClipboardPlugin(nil, zap.NewNop(), false)
	setProbe(t, p, func() (clipboardBackend, string) { return backendWayland, "wayland-1" })

	shimWith(t, "wl-paste", "#!/bin/sh\nprintf 'hello world'\n")
	dev := &fakeSender{}

	if err := Push(context.Background(), dev, p); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(dev.sent) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(dev.sent))
	}
	var body ClipboardBody
	if err := json.Unmarshal(dev.sent[0].Body, &body); err != nil {
		t.Fatalf("bad body: %v", err)
	}
	if body.Content != "hello world" {
		t.Fatalf("expected 'hello world', got %q", body.Content)
	}
}

// TestPush_DoesNotEchoReceivedClipboard guards the echo bug: after the phone
// pushes content, the local clipboard holds it (possibly with a trailing
// newline appended by wl-copy). A --watch-triggered Push must NOT send that
// same content straight back to the phone.
func TestPush_DoesNotEchoReceivedClipboard(t *testing.T) {
	p := NewClipboardPlugin(nil, zap.NewNop(), false)
	setProbe(t, p, func() (clipboardBackend, string) { return backendWayland, "wayland-1" })

	// Phone sent "hello"; wl-copy stored "hello\n" (no -n); wl-paste reads it
	// back with the trailing newline still attached.
	p.mu.Lock()
	p.lastContent = "hello"
	p.mu.Unlock()
	shimWith(t, "wl-paste", "#!/bin/sh\nprintf 'hello\\n'\n")
	dev := &fakeSender{}

	if err := Push(context.Background(), dev, p); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(dev.sent) != 0 {
		t.Fatalf("expected no echo packet, got %d", len(dev.sent))
	}

	// Exact round-trip (what wl-copy -n produces) must also be suppressed.
	p.mu.Lock()
	p.lastContent = "new clip"
	p.mu.Unlock()
	shimWith(t, "wl-paste", "#!/bin/sh\nprintf 'new clip'\n")
	dev = &fakeSender{}

	if err := Push(context.Background(), dev, p); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(dev.sent) != 0 {
		t.Fatalf("expected no echo packet, got %d", len(dev.sent))
	}

	// Genuinely different content must still be pushed.
	shimWith(t, "wl-paste", "#!/bin/sh\nprintf 'something else'\n")
	if err := Push(context.Background(), dev, p); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(dev.sent) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(dev.sent))
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
