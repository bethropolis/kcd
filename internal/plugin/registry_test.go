package plugin

import (
	"context"
	"crypto/x509"
	"net"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// --- mocks ---

type mockPlugin struct {
	name          string
	incomingTypes []string
	outgoingTypes []string
	timeout       time.Duration
	handle        func(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error
}

func (m *mockPlugin) Name() string                 { return m.name }
func (m *mockPlugin) Timeout() time.Duration       { return m.timeout }
func (m *mockPlugin) IncomingTypes() []string      { return m.incomingTypes }
func (m *mockPlugin) OutgoingTypes() []string      { return m.outgoingTypes }
func (m *mockPlugin) OnConnect(_ device.Sender)    {}
func (m *mockPlugin) OnDisconnect(_ device.Sender) {}

func (m *mockPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	if m.handle != nil {
		return m.handle(ctx, dev, pkt)
	}
	return nil
}

type mockSender struct {
	id string
}

func (m *mockSender) ID() string                     { return m.id }
func (m *mockSender) Name() string                   { return "mock" }
func (m *mockSender) SetName(_ string)               {}
func (m *mockSender) State() device.PairingState     { return device.StatePaired }
func (m *mockSender) SetState(_ device.PairingState) {}
func (m *mockSender) Send(_ *protocol.Packet) error  { return nil }
func (m *mockSender) IsConnected() bool              { return true }
func (m *mockSender) RemoteIP() net.IP               { return nil }
func (m *mockSender) PeerCert() *x509.Certificate    { return nil }
func (m *mockSender) HasCapability(_ string) bool    { return false }
func (m *mockSender) UpdateBattery(_ int, _ bool)    {}
func (m *mockSender) GetBattery() (int, bool)        { return 0, false }

// --- tests ---

func TestDispatch_FastPlugin_ReturnsTrue(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	r := NewRegistry(logger)

	r.Register(&mockPlugin{
		name:          "fast",
		incomingTypes: []string{"test.fast"},
		timeout:       5 * time.Second,
		handle: func(_ context.Context, _ device.Sender, _ *protocol.Packet) error {
			return nil
		},
	})

	pkt := protocol.AcquirePacket()
	pkt.Type = "test.fast"

	ok := r.Dispatch(context.Background(), &mockSender{id: "dev1"}, pkt)
	if !ok {
		t.Error("Dispatch returned false for a fast plugin, expected true")
	}
}

func TestDispatch_SlowPluginTimeout_ReturnsFalse(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	r := NewRegistry(logger)

	slowDone := make(chan struct{})

	r.Register(&mockPlugin{
		name:          "slow",
		incomingTypes: []string{"test.slow"},
		timeout:       10 * time.Millisecond, // very short timeout
		handle: func(ctx context.Context, _ device.Sender, _ *protocol.Packet) error {
			defer close(slowDone)
			<-ctx.Done()
			return ctx.Err()
		},
	})

	pkt := protocol.AcquirePacket()
	pkt.Type = "test.slow"

	ok := r.Dispatch(context.Background(), &mockSender{id: "dev2"}, pkt)
	if ok {
		t.Error("Dispatch returned true for a timed-out plugin, expected false")
	}

	// Wait for the background goroutine to finish so we don't leak it.
	select {
	case <-slowDone:
	case <-time.After(time.Second):
		t.Fatal("background goroutine did not finish within 1s")
	}
}

func TestDispatch_UnregisteredType_ReturnsTrue(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	r := NewRegistry(logger)

	pkt := protocol.AcquirePacket()
	pkt.Type = "no.such.type"

	ok := r.Dispatch(context.Background(), &mockSender{id: "dev3"}, pkt)
	if !ok {
		t.Error("Dispatch returned false for unregistered type, expected true")
	}
}
