package findthisdevice

import (
	"context"
	"testing"

	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/testutil"
)

func TestFindThisDevicePlugin_SingleFlight(t *testing.T) {
	bus := events.NewBus(log.Nop())
	p := NewFindThisDevicePlugin(bus, log.Nop())
	dev := testutil.NewFakeSender("dev1")

	pkt, err := protocol.NewPacket("kdeconnect.findmyphone.request", map[string]any{})
	if err != nil {
		t.Fatalf("NewPacket: %v", err)
	}

	// Manually mark as playing
	p.playing.Store(true)

	// Second request should be ignored and return nil immediately
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle returned error when playing: %v", err)
	}

	// After done playing, should accept
	p.playing.Store(false)
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle returned error when idle: %v", err)
	}
}
