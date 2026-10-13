package lockdevice

import (
	"crypto/x509"
	"encoding/json"
	"net"
	"testing"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
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
	p := NewLockDevicePlugin(log.Nop())
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
		var body LockBody
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
