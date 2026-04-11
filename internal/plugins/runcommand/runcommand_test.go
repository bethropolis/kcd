package runcommand

import (
	"context"
	"testing"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap/zaptest"
)

func TestRunCommandPlugin_Handle(t *testing.T) {
	logger := zaptest.NewLogger(t)
	dev := device.NewDevice("dev1", "Test", "phone", logger)
	p := &RunCommandPlugin{
		Commands: map[string]string{"key1": "echo test"},
	}

	body := RequestBody{
		Key: "key1",
	}
	pkt, _ := protocol.NewPacket("kdeconnect.runcommand.request", body)

	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}
}
