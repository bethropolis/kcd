package lockdevice

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/sysexec"
	"github.com/bethropolis/kcd/internal/testutil"
)

type captureSender struct {
	sent []*protocol.Packet
}

func (s *captureSender) ID() string                    { return "phone1" }
func (s *captureSender) Name() string                  { return "" }
func (s *captureSender) SetName(string)                {}
func (s *captureSender) State() device.PairingState    { return device.StatePaired }
func (s *captureSender) SetState(device.PairingState)  {}
func (s *captureSender) Send(p *protocol.Packet) error { s.sent = append(s.sent, p); return nil }
func (s *captureSender) IsConnected() bool             { return true }
func (s *captureSender) RemoteIP() net.IP              { return nil }
func (s *captureSender) PeerCert() *x509.Certificate   { return nil }
func (s *captureSender) HasCapability(string) bool     { return false }
func (s *captureSender) UpdateBattery(int, bool)       {}
func (s *captureSender) GetBattery() (int, bool)       { return 0, false }

// Lock/Unlock address the remote device over the wire. They must never
// touch the local session — a regression here locks the operator's own
// workstation while reporting success for the phone.
func TestLockUnlockSendRequestPackets(t *testing.T) {
	p := NewLockDevicePlugin(config.LockDeviceConfig{}, nil, log.Nop())
	dev := &captureSender{}

	if err := p.Lock(dev); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if err := p.Unlock(dev); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	if len(dev.sent) != 2 {
		t.Fatalf("sent %d packets, want 2", len(dev.sent))
	}
	for i, want := range []struct {
		pktType   string
		setLocked bool
	}{
		{"kdeconnect.lock.request", true},
		{"kdeconnect.lock.request", false},
	} {
		pkt := dev.sent[i]
		if pkt.Type != want.pktType {
			t.Errorf("packet %d type = %q, want %q", i, pkt.Type, want.pktType)
		}
		var body struct {
			SetLocked bool `json:"setLocked"`
		}
		if err := json.Unmarshal(pkt.Body, &body); err != nil {
			t.Fatalf("packet %d body is not JSON: %v", i, err)
		}
		if body.SetLocked != want.setLocked {
			t.Errorf("packet %d setLocked = %v, want %v", i, body.SetLocked, want.setLocked)
		}
	}

	// Unlock must carry an explicit false: an omitted field reads as false
	// anyway, but a strict peer distinguishes "unlock" from malformed.
	var raw map[string]any
	if err := json.Unmarshal(dev.sent[1].Body, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["setLocked"]; !ok {
		t.Error("unlock packet omits setLocked; it must be explicit")
	}
}

func TestUnlockedStateIncludesFalse(t *testing.T) {
	data, err := json.Marshal(outboundLockState{IsLocked: false})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if value, ok := body["isLocked"]; !ok || value != false {
		t.Fatalf("unlocked state JSON = %s, want isLocked:false", data)
	}
}

func lockPacket(t *testing.T, typ string, body any) *protocol.Packet {
	t.Helper()
	pkt, err := protocol.NewPacket(typ, body)
	if err != nil {
		t.Fatal(err)
	}
	return pkt
}

// A peer's state report and an incomplete request must never run a local action.
func TestHandleIgnoresStatePackets(t *testing.T) {
	runner := &recordingRunner{calls: make(chan string, 1)}
	p := NewLockDevicePlugin(config.LockDeviceConfig{AllowRemoteUnlock: true}, nil, log.Nop(), sysexec.New(runner))
	dev := &captureSender{}
	ctx := context.Background()

	for _, body := range []outboundLockState{{IsLocked: true}, {IsLocked: false}, {}} {
		if err := p.Handle(ctx, dev, lockPacket(t, "kdeconnect.lock", body)); err != nil {
			t.Fatalf("Handle(state %v): %v", body, err)
		}
	}
	if err := p.Handle(ctx, dev, lockPacket(t, "kdeconnect.lock.request", struct{}{})); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-runner.calls:
		t.Errorf("state or malformed request executed loginctl %q", got)
	case <-time.After(100 * time.Millisecond):
	}
	if len(dev.sent) != 0 {
		t.Errorf("state packets sent %d packets; want none", len(dev.sent))
	}
}

func TestHandlePublishesPeerLockState(t *testing.T) {
	bus := events.NewBus(log.Nop())
	sub := bus.Subscribe(1, events.TypeLockState)
	defer sub.Close()
	p := NewLockDevicePlugin(config.LockDeviceConfig{}, bus, log.Nop())
	dev := &captureSender{}
	locked := false
	if err := p.Handle(context.Background(), dev, lockPacket(t, "kdeconnect.lock", inboundLockState{IsLocked: &locked})); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-sub.C:
		if event.Type != events.TypeLockState || event.DeviceID != dev.ID() {
			t.Fatalf("lock event identity = %#v", event)
		}
		payload, ok := event.Payload.(map[string]bool)
		if !ok || payload["isLocked"] {
			t.Fatalf("lock event payload = %#v, want isLocked:false", event.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("peer lock state did not publish an event")
	}
}

// Explicit lock requests still drive the local session.
func TestHandleActsOnLockRequest(t *testing.T) {
	runner := &recordingRunner{calls: make(chan string, 2)}
	p := NewLockDevicePlugin(config.LockDeviceConfig{AllowRemoteUnlock: true}, nil, log.Nop(), sysexec.New(runner))
	dev := testutil.NewFakeSender("device")
	ctx := context.Background()

	locked := true
	if err := p.Handle(ctx, dev, lockPacket(t, "kdeconnect.lock.request", inboundLockRequest{SetLocked: &locked})); err != nil {
		t.Fatalf("Handle(lock): %v", err)
	}
	locked = false
	if err := p.Handle(ctx, dev, lockPacket(t, "kdeconnect.lock.request", inboundLockRequest{SetLocked: &locked})); err != nil {
		t.Fatalf("Handle(unlock): %v", err)
	}
	want := map[string]bool{"loginctl lock-session": false, "loginctl unlock-session": false}
	for range 2 {
		select {
		case got := <-runner.calls:
			if _, ok := want[got]; !ok {
				t.Errorf("unexpected loginctl call %q", got)
			}
			want[got] = true
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for lock and unlock commands")
		}
	}
	for call, seen := range want {
		if !seen {
			t.Errorf("missing loginctl call %q", call)
		}
	}
}
