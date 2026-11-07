package testutil

import (
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

func TestFakeSenderRecordsPacketsAndWaitsByType(t *testing.T) {
	sender := NewFakeSender("phone")
	first, _ := protocol.NewPacket("first", nil)
	second, _ := protocol.NewPacket("wanted", nil)
	if err := sender.Send(first); err != nil {
		t.Fatal(err)
	}
	if err := sender.Send(second); err != nil {
		t.Fatal(err)
	}
	if got := sender.WaitFor("wanted", time.Second); got == nil || got.Type != second.Type || string(got.Body) != string(second.Body) {
		t.Fatalf("WaitFor returned %#v, want packet type %q", got, second.Type)
	}
	if got := sender.Sent(); len(got) != 2 {
		t.Fatalf("Sent count = %d, want 2", len(got))
	}
}

func TestExpectEventSkipsOtherTypes(t *testing.T) {
	bus := events.NewBus(log.Nop())
	sub := bus.Subscribe(2)
	defer sub.Close()
	bus.Publish(events.TypeBatteryUpdate, "phone", nil)
	bus.Publish(events.TypePingReceived, "phone", nil)
	if got := ExpectEvent(t, sub, events.TypePingReceived, time.Second); got.Type != events.TypePingReceived {
		t.Fatalf("event type = %q", got.Type)
	}
}
