package events

import (
	"sync/atomic"
	"testing"

	"github.com/bethropolis/kcd/internal/log"
)

func TestHasSubscribers(t *testing.T) {
	b := NewBus(log.Nop())

	if b.HasSubscribers(TypeMprisUpdate) {
		t.Fatal("reported subscriber with zero subscribers")
	}

	other := b.Subscribe(4, TypeBatteryUpdate)
	defer other.Close()
	if b.HasSubscribers(TypeMprisUpdate) {
		t.Fatal("battery subscriber matched mpris.update")
	}

	media := b.Subscribe(4, TypeMprisUpdate)
	if !b.HasSubscribers(TypeMprisUpdate) {
		t.Fatal("missed filtered mpris.update subscriber")
	}
	media.Close()
	if b.HasSubscribers(TypeMprisUpdate) {
		t.Fatal("closed subscriber still counted")
	}

	all := b.Subscribe(4)
	defer all.Close()
	if !b.HasSubscribers(TypeMprisUpdate) {
		t.Fatal("unfiltered subscriber must match every type")
	}
}

func TestOnSubscriberChange(t *testing.T) {
	b := NewBus(log.Nop())

	var calls atomic.Int32
	b.OnSubscriberChange(func() { calls.Add(1) })

	sub := b.Subscribe(4, TypeMprisUpdate)
	if n := calls.Load(); n != 1 {
		t.Fatalf("subscribe fired hook %d times, want 1", n)
	}
	sub.Close()
	if n := calls.Load(); n != 2 {
		t.Fatalf("unsubscribe fired hook %d times total, want 2", n)
	}

	// Double close is a no-op for the subscriber map and must not
	// re-fire the hook.
	sub.Close()
	if n := calls.Load(); n != 2 {
		t.Fatalf("duplicate close fired hook, total %d want 2", n)
	}
}

func TestEventsSinceAndReplayGap(t *testing.T) {
	b := NewBus(log.Nop())
	b.Publish(TypeBatteryUpdate, "dev1", map[string]any{"charge": 1})
	b.Publish(TypeBatteryUpdate, "dev1", map[string]any{"charge": 2})
	events, gap := b.EventsSince(1, TypeBatteryUpdate)
	if gap || len(events) != 1 || events[0].Seq != 2 {
		t.Fatalf("replay = %#v, gap=%v", events, gap)
	}
	for i := 0; i < eventReplayCapacity+2; i++ {
		b.Publish(TypeBatteryUpdate, "dev1", i)
	}
	_, gap = b.EventsSince(1, TypeBatteryUpdate)
	if !gap {
		t.Fatal("expected replay gap after ring overflow")
	}
}
