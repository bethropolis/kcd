package connectivity

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

func TestHandleDeduplicatesConnectivityReports(t *testing.T) {
	bus := events.NewBus(zap.NewNop())
	sub := bus.Subscribe(4, events.TypeConnectivityUpdate)
	defer sub.Close()

	plugin := NewConnectivityPlugin(bus)
	dev := testSender{id: "device-1"}
	body := ConnectivityBody{
		SignalStrengths: map[string]SignalStrength{
			"cellular": {
				NetworkType:         "LTE",
				NetworkDetailedType: "LTE",
				SignalStrength:      4,
			},
		},
	}
	pkt, err := protocol.NewPacket("kdeconnect.connectivity_report", body)
	if err != nil {
		t.Fatal(err)
	}

	if err := plugin.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, sub)

	if err := plugin.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatal(err)
	}
	expectNoEvent(t, sub)

	changed := ConnectivityBody{
		SignalStrengths: map[string]SignalStrength{
			"cellular": {
				NetworkType:         "LTE",
				NetworkDetailedType: "LTE",
				SignalStrength:      3,
			},
		},
	}
	changedPkt, err := protocol.NewPacket("kdeconnect.connectivity_report", changed)
	if err != nil {
		t.Fatal(err)
	}
	if err := plugin.Handle(context.Background(), dev, changedPkt); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, sub)
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
