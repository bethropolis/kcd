package notification

import (
	"context"
	"testing"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap/zaptest"
)

func TestNotificationPlugin_Handle(t *testing.T) {
	logger := zaptest.NewLogger(t)
	dev := device.NewDevice("dev1", "Test", "phone", logger)
	p := &NotificationPlugin{}

	body := NotificationBody{
		AppName: "TestApp; rm -rf /",
		Title:   "Hello",
		Text:    "World",
	}
	pkt, _ := protocol.NewPacket("kdeconnect.notification", body)

	// We can't easily assert on notify-send exec in unit test, 
	// but we can ensure Handle returns without error and 
	// doesn't block.
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}
}
