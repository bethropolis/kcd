package lockdevice

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// fakeLoginctl puts a recording loginctl earlier on PATH and returns a
// function reporting the invocations seen so far. Handle shells out, so
// without this the tests would lock the operator's own session.
func fakeLoginctl(t *testing.T) func() []string {
	t.Helper()
	dir := t.TempDir()
	logFile := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\necho \"$@\" >> \"" + logFile + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "loginctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return func() []string {
		b, err := os.ReadFile(logFile)
		if err != nil {
			return nil
		}
		var out []string
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if line != "" {
				out = append(out, line)
			}
		}
		return out
	}
}

func waitForCalls(t *testing.T, calls func() []string, want int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := calls(); len(got) >= want {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("saw %d loginctl calls, want at least %d", len(calls()), want)
	return nil
}

func lockPacket(t *testing.T, typ string, body any) *protocol.Packet {
	t.Helper()
	pkt, err := protocol.NewPacket(typ, body)
	if err != nil {
		t.Fatal(err)
	}
	return pkt
}

// A peer's bare state report must never act on the local session. Before
// the type gate, any isLocked packet fell into the unlock branch.
func TestHandleIgnoresStatePackets(t *testing.T) {
	p := NewLockDevicePlugin(log.Nop())
	dev := &captureSender{}
	calls := fakeLoginctl(t)
	ctx := context.Background()

	for _, body := range []LockBody{{IsLocked: true}, {IsLocked: false}, {}} {
		if err := p.Handle(ctx, dev, lockPacket(t, "kdeconnect.lock", body)); err != nil {
			t.Fatalf("Handle(state %v): %v", body, err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if got := calls(); len(got) != 0 {
		t.Errorf("state packets executed loginctl %v; want no local action", got)
	}
	if len(dev.sent) != 0 {
		t.Errorf("state packets sent %d packets; want none", len(dev.sent))
	}
}

// Explicit lock requests still drive the local session.
func TestHandleActsOnLockRequest(t *testing.T) {
	p := NewLockDevicePlugin(log.Nop())
	dev := &captureSender{}
	calls := fakeLoginctl(t)
	ctx := context.Background()

	if err := p.Handle(ctx, dev, lockPacket(t, "kdeconnect.lock.request", LockBody{SetLocked: true})); err != nil {
		t.Fatalf("Handle(lock): %v", err)
	}
	if err := p.Handle(ctx, dev, lockPacket(t, "kdeconnect.lock.request", LockBody{SetLocked: false})); err != nil {
		t.Fatalf("Handle(unlock): %v", err)
	}
	got := waitForCalls(t, calls, 2)
	// Async execution orders nothing: assert the set, not the sequence.
	want := map[string]bool{"lock-session": false, "unlock-session": false}
	for _, c := range got {
		if _, ok := want[c]; !ok {
			t.Errorf("unexpected loginctl call %q", c)
		}
		want[c] = true
	}
	for c, seen := range want {
		if !seen {
			t.Errorf("missing loginctl call %q (saw %v)", c, got)
		}
	}
}
