package mpris

import (
	"context"
	"testing"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap/zaptest"
)

func TestMPRISPlugin_Metadata(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// We can't easily test D-Bus without a session bus,
	// but we can test the plugin registration and packet types.
	p, err := NewMPRISPlugin(logger)
	if err != nil {
		t.Skip("D-Bus not available, skipping MPRIS integration test")
		return
	}

	if p.Name() != "MPRIS" {
		t.Errorf("expected name MPRIS, got %s", p.Name())
	}

	dev := device.NewDevice("dev1", "Test", "phone", logger)
	pkt, _ := protocol.NewPacket("kdeconnect.mpris.request", MPRISRequest{RequestPlayerList: true})

	// Handle should not return error even if it fails to find players
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Errorf("Handle failed: %v", err)
	}
}
