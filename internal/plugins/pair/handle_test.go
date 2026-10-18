package pair

import (
	"context"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

func TestPairedPairRequestRequiresConfirmation(t *testing.T) {
	logger := log.Nop()
	bus := events.NewBus(logger)
	sub := bus.Subscribe(1, events.TypePairRequested)
	defer sub.Close()
	devices := device.NewRegistry(bus)
	dev := device.NewDevice("phone", "Phone", "phone", logger)
	dev.SetState(device.StatePaired)
	devices.Add(dev)
	pl := NewPairPlugin(devices, nil, config.Defaults().Pairing, nil, bus, logger)
	defer pl.cancelPendingTimer(dev.ID())
	var disconnected int
	pl.SetLifecycleCallbacks(nil, func(device.Sender) { disconnected++ })
	pkt, err := protocol.NewPairPacket(protocol.PairAccept, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := dev.State(); got != device.StatePairRequestedByPeer {
		t.Fatalf("state = %v, want PairRequestedByPeer", got)
	}
	if disconnected != 1 {
		t.Fatalf("unpaired lifecycle callbacks = %d, want 1", disconnected)
	}
	select {
	case event := <-sub.C:
		if event.Type != events.TypePairRequested || event.DeviceID != dev.ID() {
			t.Fatalf("unexpected event: %+v", event)
		}
	default:
		t.Fatal("incoming confirmation request did not publish pair.requested")
	}
}

func TestPairingCompletionFiresConnectedCallback(t *testing.T) {
	logger := log.Nop()
	devices := device.NewRegistry(nil)
	dev := device.NewDevice("phone", "Phone", "phone", logger)
	dev.SetState(device.StatePairRequested)
	devices.Add(dev)
	pl := NewPairPlugin(devices, nil, config.Defaults().Pairing, nil, nil, logger)
	var connected int
	pl.SetLifecycleCallbacks(func(device.Sender) { connected++ }, nil)
	pl.pairingDone(dev)
	if connected != 1 {
		t.Fatalf("paired lifecycle callbacks = %d, want 1", connected)
	}
}

func TestPairRequestWithoutTimestampIsRejected(t *testing.T) {
	logger := log.Nop()
	bus := events.NewBus(logger)
	devices := device.NewRegistry(bus)
	dev := device.NewDevice("phone", "Phone", "phone", logger)
	devices.Add(dev)
	pl := NewPairPlugin(devices, nil, config.Defaults().Pairing, nil, bus, logger)
	pkt, err := protocol.NewPacket(protocol.TypePair, map[string]bool{"pair": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := dev.State(); got != device.StateUnpaired {
		t.Fatalf("state = %v, want Unpaired", got)
	}
}

func TestLegacyPairRequestMayOmitTimestamp(t *testing.T) {
	logger := log.Nop()
	devices := device.NewRegistry(nil)
	dev := device.NewDevice("legacy", "Legacy", "phone", logger)
	dev.SetPeerProtocolVersion(7)
	devices.Add(dev)
	pl := NewPairPlugin(devices, nil, config.Defaults().Pairing, nil, nil, logger)
	pkt, err := protocol.NewPacket(protocol.TypePair, map[string]bool{"pair": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := dev.State(); got != device.StatePairRequestedByPeer {
		t.Fatalf("legacy request state = %v, want PairRequestedByPeer", got)
	}
	pl.cancelPendingTimer(dev.ID())
}

func TestPairRequestWithStaleTimestampIsRejected(t *testing.T) {
	logger := log.Nop()
	devices := device.NewRegistry(nil)
	dev := device.NewDevice("phone", "Phone", "phone", logger)
	devices.Add(dev)
	pl := NewPairPlugin(devices, nil, config.Defaults().Pairing, nil, nil, logger)
	pkt, err := protocol.NewPairPacket(protocol.PairAccept, time.Now().Add(-time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := dev.State(); got != device.StateUnpaired {
		t.Fatalf("stale request state = %v, want Unpaired", got)
	}
}

func TestPairTimeoutExpiresOnlyExpectedPendingState(t *testing.T) {
	logger := log.Nop()
	bus := events.NewBus(logger)
	sub := bus.Subscribe(2, events.TypePairRejected)
	defer sub.Close()
	devices := device.NewRegistry(bus)
	dev := device.NewDevice("phone", "Phone", "phone", logger)
	devices.Add(dev)
	cfg := config.Defaults()
	pl := NewPairPlugin(devices, nil, cfg.Pairing, nil, bus, logger)

	pl.changeState(dev, device.StatePairRequested)
	pl.mu.Lock()
	generation := pl.pendingGeneration[dev.ID()]
	pl.mu.Unlock()
	pl.expirePending(dev.ID(), device.StatePairRequested, generation-1)
	if got := dev.State(); got != device.StatePairRequested {
		t.Fatalf("stale timer changed state to %v", got)
	}
	pl.expirePending(dev.ID(), device.StatePairRequested, generation)
	if got := dev.State(); got != device.StateUnpaired {
		t.Fatalf("expired state = %v, want Unpaired", got)
	}
	select {
	case event := <-sub.C:
		payload, ok := event.Payload.(map[string]interface{})
		if !ok || payload["reason"] != "timeout" {
			t.Fatalf("timeout event payload = %#v", event.Payload)
		}
	default:
		t.Fatal("timeout did not publish pair.rejected")
	}
}

func TestIncomingPairTimeoutHonorsAndroidWindow(t *testing.T) {
	cfg := config.Defaults()
	cfg.Pairing.TimeoutSecs = 60
	pl := NewPairPlugin(device.NewRegistry(nil), nil, cfg.Pairing, nil, nil, log.Nop())
	if got := pl.pendingTimeout(device.StatePairRequestedByPeer); got != androidIncomingPairWait {
		t.Fatalf("incoming timeout = %v, want %v", got, androidIncomingPairWait)
	}
	if got := pl.pendingTimeout(device.StatePairRequested); got != androidOutgoingPairWait {
		t.Fatalf("outgoing timeout = %v, want %v", got, androidOutgoingPairWait)
	}
}
