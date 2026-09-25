package runcommand

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// listSender is a device.Sender that answers a command-list request
// inline, the way a phone replies from its packet handler. Injected via
// onSend so the round trip stays in one goroutine-free call stack.
type listSender struct {
	id     string
	onSend func(*protocol.Packet)
	sent   int
}

func (s *listSender) ID() string                   { return s.id }
func (s *listSender) Name() string                 { return "Test" }
func (s *listSender) SetName(string)               {}
func (s *listSender) State() device.PairingState   { return device.StatePaired }
func (s *listSender) SetState(device.PairingState) {}
func (s *listSender) IsConnected() bool            { return true }
func (s *listSender) RemoteIP() net.IP             { return nil }
func (s *listSender) PeerCert() *x509.Certificate  { return nil }
func (s *listSender) HasCapability(string) bool    { return false }
func (s *listSender) UpdateBattery(int, bool)      {}
func (s *listSender) GetBattery() (int, bool)      { return 0, false }
func (s *listSender) Send(p *protocol.Packet) error {
	s.sent++
	if s.onSend != nil {
		s.onSend(p)
	}
	return nil
}

// replyWith builds a runcommand reply packet carrying commandList.
func replyWith(t *testing.T, commandList string) *protocol.Packet {
	t.Helper()
	pkt, err := protocol.NewPacket("kdeconnect.runcommand", map[string]string{"commandList": commandList})
	if err != nil {
		t.Fatal(err)
	}
	return pkt
}

func TestRequestListReturnsDeviceCommands(t *testing.T) {
	logger := log.NewTest(t)
	p := NewRunCommandPlugin(nil, nil, logger)

	sender := &listSender{id: "dev1"}
	sender.onSend = func(pkt *protocol.Packet) {
		// A phone answers the request it just received.
		if err := p.Handle(context.Background(), sender, replyWith(t,
			`{"take-photo":{"name":"Take photo","command":"camera"},"lock":{"name":"Lock","command":"x"}}`)); err != nil {
			t.Errorf("reply Handle failed: %v", err)
		}
	}

	commands, err := p.RequestList(context.Background(), sender)
	if err != nil {
		t.Fatalf("RequestList failed: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("got %d commands, want 2: %+v", len(commands), commands)
	}
	// Sorted by name so the CLI output is stable across replies.
	if commands[0].Name != "Lock" || commands[1].Name != "Take photo" {
		t.Errorf("commands not sorted by name: %+v", commands)
	}
	if commands[1].Command != "camera" {
		t.Errorf("command payload lost: %+v", commands[1])
	}
	if sender.sent != 1 {
		t.Errorf("sent %d packets, want exactly 1 request", sender.sent)
	}
}

// The waiter must be released whether the phone answers or not, or a
// second `kcd run list` would be rejected as "already in flight" forever.
func TestRequestListReleasesWaiterAfterReply(t *testing.T) {
	logger := log.NewTest(t)
	p := NewRunCommandPlugin(nil, nil, logger)

	sender := &listSender{id: "dev1"}
	sender.onSend = func(pkt *protocol.Packet) {
		_ = p.Handle(context.Background(), sender, replyWith(t, `{"a":{"name":"A","command":"a"}}`))
	}

	for i := range 2 {
		if _, err := p.RequestList(context.Background(), sender); err != nil {
			t.Fatalf("RequestList call %d failed: %v", i, err)
		}
	}

	p.Mu.RLock()
	pending := len(p.pendingLists)
	p.Mu.RUnlock()
	if pending != 0 {
		t.Errorf("%d waiters left registered after completion, want 0", pending)
	}
}

// A reply that arrives with nobody waiting must not block the device's
// read loop or crash on a nil channel.
func TestHandleListReplyWithoutWaiterDoesNotBlock(t *testing.T) {
	logger := log.NewTest(t)
	p := NewRunCommandPlugin(nil, nil, logger)
	sender := &listSender{id: "dev1"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := p.Handle(context.Background(), sender, replyWith(t, `{"a":{"name":"A","command":"a"}}`)); err != nil {
			t.Errorf("Handle failed: %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Handle blocked on a reply with no waiter")
	}
}

// A malformed list must not wedge the waiter or panic.
func TestHandleListReplyMalformedIsDropped(t *testing.T) {
	logger := log.NewTest(t)
	p := NewRunCommandPlugin(nil, nil, logger)
	sender := &listSender{id: "dev1"}

	if err := p.Handle(context.Background(), sender, replyWith(t, `not json`)); err != nil {
		t.Errorf("Handle should swallow a malformed list, got %v", err)
	}
}

// Empty commandList is a valid answer meaning "no commands".
func TestParseCommandListEmpty(t *testing.T) {
	commands, err := parseCommandList("")
	if err != nil {
		t.Fatalf("parseCommandList(\"\") failed: %v", err)
	}
	if len(commands) != 0 {
		t.Errorf("got %d commands from an empty list, want 0", len(commands))
	}
}

// An entry without a name falls back to its map key, so a sparse phone
// implementation still lists something usable.
func TestParseCommandListFallsBackToKey(t *testing.T) {
	var entries map[string]commandEntry
	if err := json.Unmarshal([]byte(`{"screenshot":{"command":"shot"}}`), &entries); err != nil {
		t.Fatal(err)
	}
	commands, err := parseCommandList(`{"screenshot":{"command":"shot"}}`)
	if err != nil {
		t.Fatalf("parseCommandList failed: %v", err)
	}
	if len(commands) != 1 || commands[0].Name != "screenshot" {
		t.Errorf("key fallback failed: %+v", commands)
	}
	if commands[0].Command != "shot" {
		t.Errorf("command lost: %+v", commands[0])
	}
}

// A second concurrent request for the same device must fail fast rather
// than race for the single reply.
func TestRequestListRejectsConcurrentRequest(t *testing.T) {
	logger := log.NewTest(t)
	p := NewRunCommandPlugin(nil, nil, logger)

	// No onSend: the first request parks until its context is cancelled.
	sender := &listSender{id: "dev1"}

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	errCh := make(chan error, 1)
	go func() {
		_, err := p.RequestList(firstCtx, sender)
		errCh <- err
	}()

	// Give the first request time to register its waiter.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.Mu.RLock()
		_, busy := p.pendingLists["dev1"]
		p.Mu.RUnlock()
		if busy {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if _, err := p.RequestList(context.Background(), sender); err == nil {
		t.Fatal("second concurrent RequestList should fail, got nil error")
	}
	cancelFirst()
	<-errCh
}
