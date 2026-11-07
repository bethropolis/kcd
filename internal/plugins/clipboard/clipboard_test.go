package clipboard

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/testutil"
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
	p := NewClipboardPlugin(nil, log.Nop(), false)

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
	p := NewClipboardPlugin(nil, log.Nop(), false)
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
	p := NewClipboardPlugin(nil, log.Nop(), false)

	out, err := p.runClipboard(context.Background(), exec.CommandContext(context.Background(), "/bin/sh", "-c", "printf hello"))
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if string(out) != "hello" {
		t.Fatalf("expected 'hello', got %q", out)
	}
}

func TestRunClipboard_WrapsStderr(t *testing.T) {
	p := NewClipboardPlugin(nil, log.Nop(), false)

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
	p := NewClipboardPlugin(nil, log.Nop(), false)

	// A hung subprocess must be killed by the 2s timeout — and not instantly
	// (that would regress to the old premature-cancel bug).
	//
	// Two shapes of "hung" are covered: a plain child (sh -c 'sleep 30') and
	// a child that forks a grandchild still holding the stdout pipe
	// (sh -c 'sleep 30 & wait'). The grandchild survives the parent's SIGKILL,
	// so without a WaitDelay bound Output() would block on the pipe until the
	// grandchild exits — pinning runClipboard open for the whole 30s.
	for _, cmd := range []*exec.Cmd{
		exec.CommandContext(context.Background(), "/bin/sh", "-c", "sleep 30"),
		exec.CommandContext(context.Background(), "/bin/sh", "-c", "sleep 30 & wait"),
	} {
		start := time.Now()
		_, err := p.runClipboard(context.Background(), cmd)
		if err == nil {
			t.Fatal("expected a timeout error from a hung clipboard subprocess")
		}
		if elapsed := time.Since(start); elapsed < time.Second {
			t.Fatalf("subprocess killed too early (instant cancel regression?), elapsed=%v", elapsed)
		} else if elapsed > 3*clipboardTimeout {
			t.Fatalf("expected subprocess killed within ~2s, took %v", elapsed)
		}
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
	p := NewClipboardPlugin(nil, log.Nop(), false)
	setProbe(t, p, func() (clipboardBackend, string) { return backendWayland, "wayland-1" })

	// Empty clipboard with nothing copied: wl-paste exits non-zero with a
	// "no selection" diagnostic — push must not fail the CLI for this.
	shimWith(t, "wl-paste", "#!/bin/sh\necho 'No selection' >&2\nexit 1\n")
	dev := testutil.NewFakeSender("fake")

	if err := Push(context.Background(), dev, p); err != nil {
		t.Fatalf("expected soft pass on empty clipboard, got %v", err)
	}
	if len(dev.Sent()) != 0 {
		t.Fatalf("expected no packet for empty clipboard, got %d", len(dev.Sent()))
	}
}

func TestPush_EmptyOutputSendsNothing(t *testing.T) {
	p := NewClipboardPlugin(nil, log.Nop(), false)
	setProbe(t, p, func() (clipboardBackend, string) { return backendWayland, "wayland-1" })

	// Tool reports success but no content — still nothing to push.
	shimWith(t, "wl-paste", "#!/bin/sh\nexit 0\n")
	dev := testutil.NewFakeSender("fake")

	if err := Push(context.Background(), dev, p); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(dev.Sent()) != 0 {
		t.Fatalf("expected no packet for empty output, got %d", len(dev.Sent()))
	}
}

func TestPush_SendsContent(t *testing.T) {
	p := NewClipboardPlugin(nil, log.Nop(), false)
	setProbe(t, p, func() (clipboardBackend, string) { return backendWayland, "wayland-1" })

	shimWith(t, "wl-paste", "#!/bin/sh\nprintf 'hello world'\n")
	dev := testutil.NewFakeSender("fake")

	if err := Push(context.Background(), dev, p); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(dev.Sent()) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(dev.Sent()))
	}
	var body ClipboardBody
	if err := json.Unmarshal(dev.Sent()[0].Body, &body); err != nil {
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
	p := NewClipboardPlugin(nil, log.Nop(), false)
	setProbe(t, p, func() (clipboardBackend, string) { return backendWayland, "wayland-1" })

	// Phone sent "hello"; wl-copy stored "hello\n" (no -n); wl-paste reads it
	// back with the trailing newline still attached.
	p.mu.Lock()
	p.lastContent = "hello"
	p.mu.Unlock()
	shimWith(t, "wl-paste", "#!/bin/sh\nprintf 'hello\\n'\n")
	dev := testutil.NewFakeSender("fake")

	if err := Push(context.Background(), dev, p); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(dev.Sent()) != 0 {
		t.Fatalf("expected no echo packet, got %d", len(dev.Sent()))
	}

	// Exact round-trip (what wl-copy -n produces) must also be suppressed.
	p.mu.Lock()
	p.lastContent = "new clip"
	p.mu.Unlock()
	shimWith(t, "wl-paste", "#!/bin/sh\nprintf 'new clip'\n")
	dev = testutil.NewFakeSender("fake")

	if err := Push(context.Background(), dev, p); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(dev.Sent()) != 0 {
		t.Fatalf("expected no echo packet, got %d", len(dev.Sent()))
	}

	// Genuinely different content must still be pushed.
	shimWith(t, "wl-paste", "#!/bin/sh\nprintf 'something else'\n")
	if err := Push(context.Background(), dev, p); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(dev.Sent()) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(dev.Sent()))
	}
}

func TestClipboardPlugin_Handle(t *testing.T) {
	logger := log.Nop()
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

func TestClipboardPlugin_OutgoingTypes(t *testing.T) {
	p := NewClipboardPlugin(nil, log.Nop(), false)
	types := p.OutgoingTypes()
	wantTypes := map[string]bool{
		"kdeconnect.clipboard":         false,
		"kdeconnect.clipboard.connect": false,
	}
	for _, typ := range types {
		if _, ok := wantTypes[typ]; ok {
			wantTypes[typ] = true
		}
	}
	for typ, found := range wantTypes {
		if !found {
			t.Errorf("expected %q in OutgoingTypes, got %v", typ, types)
		}
	}
}

func TestClipboardPlugin_HandleConnect_Timestamps(t *testing.T) {
	logger := log.Nop()
	dev := device.NewDevice("dev1", "Test", "phone", logger)
	p := NewClipboardPlugin(nil, logger, false)
	setProbe(t, p, func() (clipboardBackend, string) { return backendUnknown, "" })

	// Set local timestamp to 1000.
	p.SetLocalChangedAt(1000)

	// Case 1: timestamp == 0 must be ignored per spec.
	pkt0, _ := protocol.NewPacket("kdeconnect.clipboard.connect", ClipboardBody{
		Content:   "ignore-zero",
		Timestamp: 0,
	})
	if err := p.Handle(context.Background(), dev, pkt0); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if p.LocalChangedAt() != 1000 {
		t.Errorf("expected localChangedAt 1000, got %d", p.LocalChangedAt())
	}
	p.mu.Lock()
	lastContent := p.lastContent
	p.mu.Unlock()
	if lastContent == "ignore-zero" {
		t.Errorf("content with timestamp 0 was accepted")
	}

	// Case 2: timestamp < localChangedAt (e.g. 500 < 1000) must be ignored.
	pktOlder, _ := protocol.NewPacket("kdeconnect.clipboard.connect", ClipboardBody{
		Content:   "ignore-older",
		Timestamp: 500,
	})
	if err := p.Handle(context.Background(), dev, pktOlder); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	p.mu.Lock()
	lastContent = p.lastContent
	p.mu.Unlock()
	if lastContent == "ignore-older" {
		t.Errorf("content with older timestamp was accepted")
	}

	// Case 3: timestamp >= localChangedAt (e.g. 2000 >= 1000) must be accepted.
	pktNewer, _ := protocol.NewPacket("kdeconnect.clipboard.connect", ClipboardBody{
		Content:   "accept-newer",
		Timestamp: 2000,
	})
	if err := p.Handle(context.Background(), dev, pktNewer); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	p.mu.Lock()
	lastContent = p.lastContent
	p.mu.Unlock()
	if lastContent != "accept-newer" {
		t.Errorf("expected lastContent 'accept-newer', got %q", lastContent)
	}
	if p.LocalChangedAt() != 2000 {
		t.Errorf("expected localChangedAt 2000, got %d", p.LocalChangedAt())
	}
}

func TestClipboardPlugin_OnConnect_SendsLocalChangedAt(t *testing.T) {
	dir := t.TempDir()
	// Fake tool returning "local text"
	script := "#!/bin/sh\nprintf 'local text'\n"
	if err := os.WriteFile(filepath.Join(dir, "wl-paste"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake wl-paste: %v", err)
	}
	t.Setenv("PATH", dir)

	logger := log.Nop()
	sender := testutil.NewFakeSender("dev1")
	p := NewClipboardPlugin(nil, logger, true)
	setProbe(t, p, func() (clipboardBackend, string) { return backendWayland, "" })

	p.SetLocalChangedAt(123456789)
	p.OnConnect(sender)

	sent := sender.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 packet sent, got %d", len(sent))
	}
	pkt := sent[0]
	if pkt.Type != "kdeconnect.clipboard.connect" {
		t.Fatalf("packet type = %q, want kdeconnect.clipboard.connect", pkt.Type)
	}
	var body ClipboardBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body.Content != "local text" {
		t.Errorf("body.Content = %q, want 'local text'", body.Content)
	}
	if body.Timestamp != 123456789 {
		t.Errorf("body.Timestamp = %d, want 123456789", body.Timestamp)
	}
}

func TestClipboardPlugin_OnConnect_SkipsEmptyClipboard(t *testing.T) {
	dir := t.TempDir()
	// Fake tool returning nothing
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "wl-paste"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake wl-paste: %v", err)
	}
	t.Setenv("PATH", dir)

	logger := log.Nop()
	sender := testutil.NewFakeSender("dev1")
	p := NewClipboardPlugin(nil, logger, true)
	setProbe(t, p, func() (clipboardBackend, string) { return backendWayland, "" })

	p.OnConnect(sender)

	if len(sender.Sent()) != 0 {
		t.Fatalf("expected 0 packets sent when clipboard is empty, got %d", len(sender.Sent()))
	}
}
