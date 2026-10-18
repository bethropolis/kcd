package testutil

import (
	"crypto/x509"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
)

// FakeSender records packets sent by a plugin and supplies configurable device state.
type FakeSender struct {
	mu         sync.Mutex
	IDValue    string
	NameValue  string
	StateValue device.PairingState
	Connected  bool
	IP         net.IP
	Cert       *x509.Certificate
	Caps       map[string]bool
	Charge     int
	Charging   bool
	sent       chan struct{}
	packets    []*protocol.Packet
}

var _ device.Sender = (*FakeSender)(nil)

// NewFakeSender creates a connected sender with initialized notification state.
func NewFakeSender(id string) *FakeSender {
	return &FakeSender{IDValue: id, StateValue: device.StatePaired, Connected: true, Caps: make(map[string]bool), sent: make(chan struct{}, 1)}
}

func (f *FakeSender) ID() string          { return f.IDValue }
func (f *FakeSender) Name() string        { f.mu.Lock(); defer f.mu.Unlock(); return f.NameValue }
func (f *FakeSender) SetName(name string) { f.mu.Lock(); f.NameValue = name; f.mu.Unlock() }
func (f *FakeSender) State() device.PairingState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.StateValue
}
func (f *FakeSender) SetState(state device.PairingState) {
	f.mu.Lock()
	f.StateValue = state
	f.mu.Unlock()
}
func (f *FakeSender) Send(pkt *protocol.Packet) error {
	f.mu.Lock()
	copyPacket := *pkt
	copyPacket.Body = append([]byte(nil), pkt.Body...)
	if pkt.PayloadTransferInfo != nil {
		transfer := *pkt.PayloadTransferInfo
		copyPacket.PayloadTransferInfo = &transfer
	}
	f.packets = append(f.packets, &copyPacket)
	f.mu.Unlock()
	select {
	case f.sent <- struct{}{}:
	default:
	}
	return nil
}
func (f *FakeSender) IsConnected() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.Connected }
func (f *FakeSender) RemoteIP() net.IP {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append(net.IP(nil), f.IP...)
}
func (f *FakeSender) PeerCert() *x509.Certificate { f.mu.Lock(); defer f.mu.Unlock(); return f.Cert }
func (f *FakeSender) HasCapability(cap string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Caps[cap]
}
func (f *FakeSender) UpdateBattery(charge int, charging bool) {
	f.mu.Lock()
	f.Charge, f.Charging = charge, charging
	f.mu.Unlock()
}
func (f *FakeSender) GetBattery() (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Charge, f.Charging
}

// Sent returns a snapshot of packets recorded so far.
func (f *FakeSender) Sent() []*protocol.Packet {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*protocol.Packet(nil), f.packets...)
}

// ResetSent clears packets recorded before the call.
func (f *FakeSender) ResetSent() {
	f.mu.Lock()
	f.packets = nil
	f.mu.Unlock()
}

// WaitFor waits until a packet of the requested type is recorded.
func (f *FakeSender) WaitFor(packetType string, timeout time.Duration) *protocol.Packet {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		for _, pkt := range f.Sent() {
			if pkt.Type == packetType {
				return pkt
			}
		}
		select {
		case <-f.sent:
		case <-deadline.C:
			return nil
		}
	}
}

// ExpectEvent waits for an event with the requested type.
func ExpectEvent(t *testing.T, sub *events.Subscriber, eventType events.EventType, timeout time.Duration) events.Event {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-sub.C:
			if !ok {
				t.Fatalf("event subscriber closed before %q arrived", eventType)
			}
			if ev.Type == eventType {
				return ev
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for event %q", eventType)
		}
	}
}
