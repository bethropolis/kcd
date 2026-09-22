package events

import (
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
