package battery

import (
	"context"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap/zaptest"
)

func newPlugin(t *testing.T) (*BatteryPlugin, *events.Bus) {
	t.Helper()
	logger := zaptest.NewLogger(t)
	bus := events.NewBus(logger)
	cfg := config.BatteryConfig{}
	cfg.Defaults()
	return NewBatteryPlugin(cfg, bus, logger), bus
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

func TestBatteryPlugin_Handle_RequestResponds(t *testing.T) {
	logger := zaptest.NewLogger(t)
	p, _ := newPlugin(t)
	dev := device.NewDevice("dev1", "Test Phone", "phone", logger)

	pkt, _ := protocol.NewPacket("kdeconnect.battery.request", map[string]bool{"request": true})
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	// No assertion on the response — it depends on whether this machine has
	// a battery. The important thing is that Handle doesn't crash or return
	// an error. The send is best-effort.
}

func TestLocalBatteryRead(t *testing.T) {
	charge, charging, err := readLocalBattery()
	if err != nil {
		// Desktops often have no battery — this is non-fatal.
		t.Logf("no local battery (expected on desktops): %v", err)
		return
	}
	if charge < 0 || charge > 100 {
		t.Errorf("battery charge out of range: %d", charge)
	}
	t.Logf("local battery: %d%% (charging=%v)", charge, charging)
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
