package notification

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap/zaptest"
)

func newPlugin(t *testing.T) *NotificationPlugin {
	t.Helper()
	logger := zaptest.NewLogger(t)
	bus := events.NewBus(logger)
	cfg := config.NotificationPluginConfig{}
	cfg.Defaults()
	// tlsConfig is nil — icon fetching is skipped in unit tests.
	p := NewNotificationPlugin(cfg, bus, nil, logger)
	t.Cleanup(p.Close)
	// Never touch the real notification daemon from tests.
	p.newExec = func(_ context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(context.Background(), "true")
	}
	return p
}

func TestNotificationPlugin_Handle_Normal(t *testing.T) {
	p := newPlugin(t)
	logger := zaptest.NewLogger(t)
	dev := device.NewDevice("dev1", "Test", "phone", logger)

	body := NotificationBody{
		AppName: "TestApp; rm -rf /", // Verify sanitisation doesn't panic.
		Title:   "Hello",
		Text:    "World",
	}
	pkt, _ := protocol.NewPacket("kdeconnect.notification", body)
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
}

func TestNotificationPlugin_Handle_Cancel(t *testing.T) {
	p := newPlugin(t)
	// Immediate close — no grace debounce for this test.
	p.cfg.CancelGraceMS = 0
	logger := zaptest.NewLogger(t)
	dev := device.NewDevice("dev1", "Test", "phone", logger)

	// Store a fake desktop ID so the cancel path can look it up.
	p.notifIDs.Store(p.notifKey(dev.ID(), "notif-abc"), "42")

	body := NotificationBody{
		ID:       "notif-abc",
		IsCancel: true,
	}
	pkt, _ := protocol.NewPacket("kdeconnect.notification", body)
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	// Entry should have been removed.
	if _, ok := p.notifIDs.Load(p.notifKey(dev.ID(), "notif-abc")); ok {
		t.Error("expected notifIDs entry to be removed after cancel")
	}
}

func TestNotificationPlugin_Handle_Silent(t *testing.T) {
	p := newPlugin(t)
	logger := zaptest.NewLogger(t)
	dev := device.NewDevice("dev1", "Test", "phone", logger)

	body := NotificationBody{
		AppName: "SilentApp",
		Title:   "Quiet",
		Silent:  true,
	}
	pkt, _ := protocol.NewPacket("kdeconnect.notification", body)
	// Silent notifications must be accepted without error and produce no output.
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
}

// fakeNotifier records the notify-send invocations and fakes a print-id.
type fakeNotifier struct {
	mu     sync.Mutex
	calls  [][]string
	nextID int
}

func (f *fakeNotifier) command(name string, args ...string) *exec.Cmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{name}, args...))
	f.nextID++
	id := strconv.Itoa(f.nextID)
	// Simulate notify-send --print-id printing the new desktop id.
	return exec.CommandContext(context.Background(), "sh", "-c", "printf '%s' '"+id+"'")
}

func (f *fakeNotifier) argFor(call int, flag string) string {
	for i, a := range f.calls[call] {
		if a == flag {
			if i+1 < len(f.calls[call]) {
				return f.calls[call][i+1]
			}
		}
	}
	return ""
}

func newFakePlugin(t *testing.T, replace bool) (*NotificationPlugin, *fakeNotifier) {
	t.Helper()
	logger := zaptest.NewLogger(t)
	bus := events.NewBus(logger)
	cfg := config.NotificationPluginConfig{}
	cfg.Defaults()
	cfg.ReplaceNotifications = replace
	p := NewNotificationPlugin(cfg, bus, nil, logger)
	t.Cleanup(p.Close)
	p.canCloseNotifs = true
	f := &fakeNotifier{}
	p.newExec = func(_ context.Context, name string, args ...string) *exec.Cmd {
		return f.command(name, args...)
	}
	return p, f
}

func TestNotificationPlugin_ReplaceByID(t *testing.T) {
	p, f := newFakePlugin(t, true)
	dev := device.NewDevice("dev1", "Test", "phone", zaptest.NewLogger(t))
	id := "0|com.arn.scrobble|0|com.msob7y.namida|10247"

	// First post — no replace, stores the printed desktop id.
	p.sendDesktopNotification(dev.ID(), "Pano Scrobbler", id, "Song", "Artist", "")
	if r := f.argFor(0, "-r"); r != "" {
		t.Fatalf("expected no replace on first post, got -r %q", r)
	}
	key := p.notifKey(dev.ID(), id)
	if v, ok := p.notifIDs.Load(key); !ok || v != "1" {
		t.Fatalf("expected stored desktop id 1 under %q, got %v/%v", key, ok, v)
	}

	// Second post, same id — must replace the previous popup.
	p.sendDesktopNotification(dev.ID(), "Pano Scrobbler", id, "Song", "Artist", "")
	if r := f.argFor(1, "-r"); r != "1" {
		t.Fatalf("expected -r 1 on update, got %q", r)
	}
	if v, ok := p.notifIDs.Load(key); !ok || v != "2" {
		t.Fatalf("expected stored desktop id 2, got %v/%v", ok, v)
	}

	// Same id from a different device must not replace this device's popup.
	other := device.NewDevice("dev2", "Other", "phone", zaptest.NewLogger(t))
	p.sendDesktopNotification(other.ID(), "Pano Scrobbler", id, "Song", "Artist", "")
	if r := f.argFor(2, "-r"); r != "" {
		t.Fatalf("expected no replace for a different device, got -r %q", r)
	}
	if v, ok := p.notifIDs.Load(p.notifKey(other.ID(), id)); !ok || v != "3" {
		t.Fatalf("expected stored desktop id 3 under dev2 key, got %v/%v", ok, v)
	}

	// After a cancel, the entry is gone and the next post is fresh again.
	p.notifIDs.Delete(key)
	p.sendDesktopNotification(dev.ID(), "Pano Scrobbler", id, "Song", "Artist", "")
	if r := f.argFor(3, "-r"); r != "" {
		t.Fatalf("expected no replace after cancel, got -r %q", r)
	}
}

func TestNotificationPlugin_ReplaceByIDDisabled(t *testing.T) {
	p, f := newFakePlugin(t, false)
	dev := device.NewDevice("dev1", "Test", "phone", zaptest.NewLogger(t))
	id := "0|com.arn.scrobble|0|com.msob7y.namida|10247"

	// Seed a stored id — with replacement disabled it must be ignored.
	p.notifIDs.Store(p.notifKey(dev.ID(), id), "7")

	p.sendDesktopNotification(dev.ID(), "Pano Scrobbler", id, "Song", "Artist", "")
	if r := f.argFor(0, "-r"); r != "" {
		t.Fatalf("expected no replace when config disabled, got -r %q", r)
	}
	// The print-id is still captured so cancel/close keeps working.
	if v, ok := p.notifIDs.Load(p.notifKey(dev.ID(), id)); !ok || v != "1" {
		t.Fatalf("expected stored desktop id 1, got %v/%v", ok, v)
	}
}

func TestNotificationPlugin_FetchIconReusesCacheOnRepost(t *testing.T) {
	p := newPlugin(t)
	p.tlsConfig = &tls.Config{}
	p.iconDir = t.TempDir()
	app := "Pano Scrobbler"
	id := "0|com.arn.scrobble|0|com.msob7y.namida|10247"

	// Seed the cached icon for this (app, id) — simulates the first post
	// having downloaded the phone's icon.
	cachedPath := filepath.Join(p.iconDir, fmt.Sprintf("%s-%s.png",
		nonAlphaNumeric.ReplaceAllString(app, "_"), id))
	if err := os.WriteFile(cachedPath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Re-post without an icon payload must reuse the cached file so the
	// popup keeps the real app icon instead of a placeholder.
	got := p.fetchIcon(context.Background(), app, id, nil, 0, 0, false)
	if got != cachedPath {
		t.Fatalf("expected cached icon reuse %q, got %q", cachedPath, got)
	}

	// Unknown id, no payload, no cache → empty (theme fallback downstream).
	if got := p.fetchIcon(context.Background(), app, "unknown-id", nil, 0, 0, false); got != "" {
		t.Fatalf("expected empty icon for uncached payload-less repost, got %q", got)
	}
}

func TestNotificationPlugin_ShowIconsGating(t *testing.T) {
	dev := device.NewDevice("dev1", "Test", "phone", zaptest.NewLogger(t))

	// Default (show_icons = false): an explicit empty icon — popups render
	// icon-less and daemons won't derive a placeholder from the app name.
	p, f := newFakePlugin(t, true)
	p.sendDesktopNotification(dev.ID(), "Pano Scrobbler", "id-1", "Song", "Artist", "")
	if got := f.argFor(0, "-i"); got != "" {
		t.Fatalf("expected empty -i by default, got -i %q", got)
	}

	// show_icons = true: -i carries the icon path/name.
	p2, f2 := newFakePlugin(t, true)
	p2.cfg.ShowIcons = true
	p2.sendDesktopNotification(dev.ID(), "Pano Scrobbler", "id-2", "Song", "Artist", "/tmp/icon.png")
	if got := f2.argFor(0, "-i"); got != "/tmp/icon.png" {
		t.Fatalf("expected -i /tmp/icon.png with icons enabled, got %q", got)
	}
}

// gdbusCalls returns the recorded CloseNotification invocations.
func (f *fakeNotifier) gdbusCalls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, c := range f.calls {
		if len(c) > 0 && c[0] == "gdbus" {
			out = append(out, c)
		}
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func postAndCancel(t *testing.T, p *NotificationPlugin, dev device.Sender, id string) {
	t.Helper()
	p.sendDesktopNotification(dev.ID(), "Pano Scrobbler", id, "Song", "Artist", "")
	body := NotificationBody{ID: id, IsCancel: true}
	pkt, _ := protocol.NewPacket("kdeconnect.notification", body)
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle(cancel) returned error: %v", err)
	}
}

func TestNotificationPlugin_CancelGrace_RePostWithinWindow(t *testing.T) {
	p, f := newFakePlugin(t, true)
	p.cfg.CancelGraceMS = 200
	dev := device.NewDevice("dev1", "Test", "phone", zaptest.NewLogger(t))
	id := "0|com.arn.scrobble|0|com.msob7y.namida|10247"
	key := p.notifKey(dev.ID(), id)

	// Post then cancel — the close is deferred, the popup stays open.
	postAndCancel(t, p, dev, id)
	if len(f.gdbusCalls()) != 0 {
		t.Fatalf("expected no immediate close under cancel-grace, got %d gdbus calls", len(f.gdbusCalls()))
	}
	if v, ok := p.notifIDs.Load(key); !ok || v != "1" {
		t.Fatalf("expected desktop id 1 retained during grace, got %v/%v", ok, v)
	}

	// Re-post within the window — must update in place, not close+reopen.
	p.sendDesktopNotification(dev.ID(), "Pano Scrobbler", id, "Song", "Artist", "")
	if r := f.argFor(1, "-r"); r != "1" {
		t.Fatalf("expected -r 1 on re-post during grace, got %q", r)
	}

	// Wait past the grace window: the deferred close must have been cancelled.
	time.Sleep(250 * time.Millisecond)
	if len(f.gdbusCalls()) != 0 {
		t.Fatalf("expected deferred close cancelled on re-post, got %d gdbus calls", len(f.gdbusCalls()))
	}
	if v, ok := p.notifIDs.Load(key); !ok || v != "2" {
		t.Fatalf("expected desktop id 2 after in-place update, got %v/%v", ok, v)
	}
}

func TestNotificationPlugin_CancelGrace_NoRePost(t *testing.T) {
	p, f := newFakePlugin(t, true)
	p.cfg.CancelGraceMS = 50
	dev := device.NewDevice("dev1", "Test", "phone", zaptest.NewLogger(t))
	id := "0|com.arn.scrobble|0|com.msob7y.namida|10247"
	key := p.notifKey(dev.ID(), id)

	postAndCancel(t, p, dev, id)
	if len(f.gdbusCalls()) != 0 {
		t.Fatalf("expected no immediate close under cancel-grace, got %d gdbus calls", len(f.gdbusCalls()))
	}

	// No re-post: after the grace window the popup is closed and the entry dropped.
	waitFor(t, "deferred close", func() bool {
		if len(f.gdbusCalls()) != 1 {
			return false
		}
		_, ok := p.notifIDs.Load(key)
		return !ok
	})
	if got := f.gdbusCalls()[0][len(f.gdbusCalls()[0])-1]; got != "1" {
		t.Fatalf("expected gdbus close of desktop id 1, got %q", got)
	}
}

func TestNotificationPlugin_CancelGraceDisabled(t *testing.T) {
	p, f := newFakePlugin(t, true)
	p.cfg.CancelGraceMS = 0
	dev := device.NewDevice("dev1", "Test", "phone", zaptest.NewLogger(t))
	id := "0|com.arn.scrobble|0|com.msob7y.namida|10247"
	key := p.notifKey(dev.ID(), id)

	postAndCancel(t, p, dev, id)
	waitFor(t, "immediate gdbus close", func() bool {
		if _, ok := p.notifIDs.Load(key); ok {
			return false
		}
		return len(f.gdbusCalls()) == 1
	})
	if got := f.gdbusCalls()[0][len(f.gdbusCalls()[0])-1]; got != "1" {
		t.Fatalf("expected gdbus close of desktop id 1, got %q", got)
	}
}
