package clipboard

import (
	"context"
	"testing"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap/zaptest"
)

func TestClipboardPlugin_Handle(t *testing.T) {
	logger := zaptest.NewLogger(t)
	dev := device.NewDevice("dev1", "Test", "phone", logger)
	p := &ClipboardPlugin{}

	body := ClipboardBody{
		Content: "Hello world!",
	}
	pkt, _ := protocol.NewPacket("kdeconnect.clipboard", body)

	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}
}
