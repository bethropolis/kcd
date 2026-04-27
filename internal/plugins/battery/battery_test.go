package battery

import (
	"context"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap/zaptest"
)

func newPlugin(t *testing.T) (*BatteryPlugin, *events.Bus) {
	t.Helper()
	logger := zaptest.NewLogger(t)
	bus := events.NewBus(logger)
	return NewBatteryPlugin(bus, logger), bus
}

func TestBatteryPlugin_Handle_UpdatesDevice(t *testing.T) {
	logger := zaptest.NewLogger(t)
	p, _ := newPlugin(t)
	dev := device.NewDevice("dev1", "Test Phone", "phone", logger)

	pkt, _ := protocol.NewPacket("kdeconnect.battery", BatteryBody{
		CurrentCharge: 85,
		IsCharging:    true,
	})
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	charge, charging := dev.GetBattery()
	if charge != 85 {
		t.Errorf("expected charge 85, got %d", charge)
	}
	if !charging {
		t.Error("expected charging=true")
	}
}

func TestBatteryPlugin_Handle_ThresholdLow_EmitsEvent(t *testing.T) {
	logger := zaptest.NewLogger(t)
	p, bus := newPlugin(t)
	dev := device.NewDevice("dev1", "Test Phone", "phone", logger)

	sub := bus.Subscribe(0, events.TypeBatteryThreshold)
	defer sub.Close()

	pkt, _ := protocol.NewPacket("kdeconnect.battery", BatteryBody{
		CurrentCharge:  12,
		IsCharging:     false,
		ThresholdEvent: thresholdLow,
	})
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	// handleThreshold runs in a goroutine — give it a moment.
	select {
	case evt := <-sub.C:
		if evt.Type != events.TypeBatteryThreshold {
			t.Errorf("expected battery.threshold event, got %s", evt.Type)
		}
		payload, ok := evt.Payload.(map[string]any)
		if !ok {
			t.Fatalf("unexpected payload type %T", evt.Payload)
		}
		if payload["event"] != thresholdLow {
			t.Errorf("expected thresholdLow (%d), got %v", thresholdLow, payload["event"])
		}
	case <-time.After(2 * time.Second):
		t.Error("timed out waiting for battery.threshold event")
	}
}

func TestBatteryPlugin_Handle_ThresholdFull_EmitsEvent(t *testing.T) {
	logger := zaptest.NewLogger(t)
	p, bus := newPlugin(t)
	dev := device.NewDevice("dev1", "Test Phone", "phone", logger)

	sub := bus.Subscribe(0, events.TypeBatteryThreshold)
	defer sub.Close()

	pkt, _ := protocol.NewPacket("kdeconnect.battery", BatteryBody{
		CurrentCharge:  100,
		IsCharging:     true,
		ThresholdEvent: thresholdFull,
	})
	_ = p.Handle(context.Background(), dev, pkt)

	select {
	case evt := <-sub.C:
		payload := evt.Payload.(map[string]any)
		if payload["event"] != thresholdFull {
			t.Errorf("expected thresholdFull (%d), got %v", thresholdFull, payload["event"])
		}
	case <-time.After(2 * time.Second):
		t.Error("timed out waiting for battery.threshold event")
	}
}

func TestBatteryPlugin_Handle_NoThreshold_NoEvent(t *testing.T) {
	logger := zaptest.NewLogger(t)
	p, bus := newPlugin(t)
	dev := device.NewDevice("dev1", "Test Phone", "phone", logger)

	sub := bus.Subscribe(0, events.TypeBatteryThreshold)
	defer sub.Close()

	pkt, _ := protocol.NewPacket("kdeconnect.battery", BatteryBody{
		CurrentCharge:  50,
		IsCharging:     false,
		ThresholdEvent: thresholdNone,
	})
	_ = p.Handle(context.Background(), dev, pkt)

	select {
	case <-sub.C:
		t.Error("unexpected battery.threshold event for thresholdNone")
	case <-time.After(200 * time.Millisecond):
		// correct — no event
	}
}
