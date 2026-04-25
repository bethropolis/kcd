package notification

import (
	"context"
	"testing"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap/zaptest"
)

func newPlugin(t *testing.T) *NotificationPlugin {
	t.Helper()
	logger := zaptest.NewLogger(t)
	bus := events.NewBus(logger)
	// tlsConfig is nil — icon fetching is skipped in unit tests.
	p := NewNotificationPlugin(bus, nil, logger)
	t.Cleanup(p.Close)
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
	logger := zaptest.NewLogger(t)
	dev := device.NewDevice("dev1", "Test", "phone", logger)

	// Store a fake desktop ID so the cancel path can look it up.
	p.notifIDs.Store("notif-abc", "42")

	body := NotificationBody{
		ID:       "notif-abc",
		IsCancel: true,
	}
	pkt, _ := protocol.NewPacket("kdeconnect.notification", body)
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	// Entry should have been removed.
	if _, ok := p.notifIDs.Load("notif-abc"); ok {
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
