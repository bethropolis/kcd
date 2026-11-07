package device

import (
	"testing"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
)

func TestSendDropsNonPairPacketsUntilPaired(t *testing.T) {
	dev := NewDevice("phone", "Phone", "phone", log.Nop())
	dev.conn = &transport.Conn{}

	packet, err := protocol.NewPacket("kdeconnect.battery", struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.Send(packet); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(dev.sendChan) != 0 {
		t.Fatal("non-pair packet was queued for an unpaired device")
	}

	packet, err = protocol.NewPairPacket(protocol.PairAccept, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.Send(packet); err != nil {
		t.Fatalf("Send pair packet: %v", err)
	}
	if len(dev.sendChan) != 1 {
		t.Fatal("pair packet was not queued for an unpaired device")
	}
}
