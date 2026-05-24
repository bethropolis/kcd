package mpris

import (
	"context"
	"crypto/x509"
	"net"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

type testSender struct {
	id string
}

func (s testSender) ID() string                              { return s.id }
func (s testSender) Name() string                            { return "" }
func (s testSender) SetName(string)                          {}
func (s testSender) State() device.PairingState              { return device.StateUnpaired }
func (s testSender) SetState(device.PairingState)            {}
func (s testSender) Send(*protocol.Packet) error             { return nil }
func (s testSender) IsConnected() bool                       { return true }
func (s testSender) RemoteIP() net.IP                        { return nil }
func (s testSender) PeerCert() *x509.Certificate             { return nil }
func (s testSender) HasCapability(string) bool               { return false }
func (s testSender) UpdateBattery(charge int, charging bool) {}
func (s testSender) GetBattery() (int, bool)                 { return 0, false }

func TestHandleDeduplicatesRemoteMPRISUpdates(t *testing.T) {
	bus := events.NewBus(zap.NewNop())
	sub := bus.Subscribe(4, events.TypeMprisUpdate)
	defer sub.Close()

	plugin := NewMPRISPlugin(nil, bus, false, zap.NewNop())
	if plugin.watchCancel != nil {
		defer plugin.watchCancel()
	}
	dev := testSender{id: "device-1"}
	state := MPRISRequest{
		Player:         "Metrolist",
		Title:          "Sour Grapes",
		Artist:         "LE SSERAFIM",
		Album:          "FEARLESS",
		Pos:            1000,
		IsPlaying:      true,
		Volume:         80,
		PlaybackStatus: "Playing",
	}
	pkt := newMPRISPacket(t, state)

	if err := plugin.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, sub)

	positionOnly := state
	positionOnly.Pos = 2000
	if err := plugin.Handle(context.Background(), dev, newMPRISPacket(t, positionOnly)); err != nil {
		t.Fatal(err)
	}
	expectNoEvent(t, sub)

	got := plugin.RemoteState(dev.ID())
	if got == nil {
		t.Fatal("expected remote state")
	}
	if got.Pos < positionOnly.Pos {
		t.Fatalf("expected position tracker to update to at least %d, got %d", positionOnly.Pos, got.Pos)
	}

	changed := positionOnly
	changed.Title = "Blue Flame"
	if err := plugin.Handle(context.Background(), dev, newMPRISPacket(t, changed)); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, sub)
}

func newMPRISPacket(t *testing.T, body MPRISRequest) *protocol.Packet {
	t.Helper()
	pkt, err := protocol.NewPacket("kdeconnect.mpris", body)
	if err != nil {
		t.Fatal(err)
	}
	return pkt
}

func expectEvent(t *testing.T, sub *events.Subscriber) {
	t.Helper()
	select {
	case <-sub.C:
	case <-time.After(time.Second):
		t.Fatal("expected event")
	}
}

func expectNoEvent(t *testing.T, sub *events.Subscriber) {
	t.Helper()
	select {
	case ev := <-sub.C:
		t.Fatalf("unexpected event: %#v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}
