package battery

import (
	"context"
	"testing"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap/zaptest"
)

func TestBatteryPlugin_Handle(t *testing.T) {
	logger := zaptest.NewLogger(t)
	dev := device.NewDevice("dev1", "Test Phone", "phone", logger)
	p := &BatteryPlugin{}

	body := BatteryBody{
		CurrentCharge: 85,
		IsCharging:    true,
	}
	pkt, _ := protocol.NewPacket("kdeconnect.battery", body)

	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	charge, charging := dev.GetBattery()
	if charge != 85 {
		t.Errorf("expected charge 85, got %d", charge)
	}
	if !charging {
		t.Error("expected charging true, got false")
	}
}
