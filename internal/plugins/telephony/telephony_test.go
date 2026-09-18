package telephony

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

type stubSender struct{}

func (stubSender) ID() string                   { return "dev1" }
func (stubSender) Name() string                 { return "test" }
func (stubSender) SetName(string)               {}
func (stubSender) State() device.PairingState   { return device.StatePaired }
func (stubSender) SetState(device.PairingState) {}
func (stubSender) Send(*protocol.Packet) error  { return nil }
func (stubSender) IsConnected() bool            { return true }
func (stubSender) RemoteIP() net.IP             { return nil }
func (stubSender) PeerCert() *x509.Certificate  { return nil }
func (stubSender) HasCapability(string) bool    { return true }
func (stubSender) UpdateBattery(int, bool)      {}
func (stubSender) GetBattery() (int, bool)      { return 0, false }

func nextEvent(t *testing.T, ch <-chan events.Event) events.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for event")
		return events.Event{}
	}
}

func TestHandleStringIsCancel(t *testing.T) {
	// Stock phones end calls with the string "true", not a boolean.
	logger := zaptest.NewLogger(t)
	bus := events.NewBus(logger)
	p := NewTelephonyPlugin(bus, logger)
	sub := bus.Subscribe(0, events.TypeTelephonyCanceled)
	defer sub.Close()

	pkt := &protocol.Packet{
		Type: "kdeconnect.telephony",
		Body: json.RawMessage(`{"event":"ringing","phoneNumber":"+1","isCancel":"true"}`),
	}
	if err := p.Handle(context.Background(), stubSender{}, pkt); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if ev := nextEvent(t, sub.C); ev.Type != events.TypeTelephonyCanceled {
		t.Errorf("event type = %q, want %q", ev.Type, events.TypeTelephonyCanceled)
	}
}

func TestHandleMissedCallEvent(t *testing.T) {
	logger := zaptest.NewLogger(t)
	bus := events.NewBus(logger)
	// The plugin notifies via a background goroutine that can outlive the
	// test; a test-bound logger would panic on late writes, so detach it.
	p := NewTelephonyPlugin(bus, zap.NewNop())
	sub := bus.Subscribe(0, events.TypeTelephonyMissed)
	defer sub.Close()

	pkt := &protocol.Packet{
		Type: "kdeconnect.telephony",
		Body: json.RawMessage(`{"event":"missedCall","contactName":"Alice"}`),
	}
	if err := p.Handle(context.Background(), stubSender{}, pkt); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if ev := nextEvent(t, sub.C); ev.Type != events.TypeTelephonyMissed {
		t.Errorf("event type = %q, want %q", ev.Type, events.TypeTelephonyMissed)
	}
}
