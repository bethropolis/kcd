package events

import (
	"testing"

	"github.com/bethropolis/kcd/internal/log"
)

// All() and the constant block drift apart silently otherwise: a new event
// missing from All would go unreported, since nothing depends on the list yet
// but the next caller to use it would inherit the gap.
func TestAllMatchesConstants(t *testing.T) {
	all := All()
	seen := make(map[EventType]bool, len(all))
	for _, typ := range all {
		if seen[typ] {
			t.Errorf("All() lists %q twice", typ)
		}
		seen[typ] = true
	}

	for _, want := range allTypesFromSource(t) {
		if !seen[want] {
			t.Errorf("All() is missing %q, declared in bus.go", want)
		}
	}
	if len(all) != len(allTypesFromSource(t)) {
		t.Errorf("All() has %d types, bus.go declares %d", len(all), len(allTypesFromSource(t)))
	}
}

// The core of the SMS opt-in: an unfiltered subscriber must not match SMS.
// HasSubscribers arms the phone on this same predicate, so a match here is
// permission to start the push as well as to read the body.
func TestUnfilteredSubscriberSkipsOptInTypes(t *testing.T) {
	bus := NewBus(log.Nop())
	sub := bus.Subscribe(1)
	defer sub.Close()

	for _, typ := range OptInOnly() {
		if sub.matches(typ) {
			t.Errorf("unfiltered subscriber matches %q", typ)
		}
		if bus.HasSubscribers(typ) {
			t.Errorf("HasSubscribers(%q) = true; the phone would be armed", typ)
		}
	}
	for _, typ := range All() {
		if isOptInOnly(typ) {
			continue
		}
		if !sub.matches(typ) {
			t.Errorf("unfiltered subscriber does not match %q", typ)
		}
	}
}

func TestNamingOptInTypeMatchesIt(t *testing.T) {
	bus := NewBus(log.Nop())
	sub := bus.Subscribe(1, TypeSMSIncoming)
	defer sub.Close()

	if !sub.matches(TypeSMSIncoming) {
		t.Error("naming sms.incoming did not match")
	}
	if !bus.HasSubscribers(TypeSMSIncoming) {
		t.Error("HasSubscribers(sms.incoming) = false; naming it must arm the phone")
	}
	if sub.matches(TypeSMSAttachment) {
		t.Error("naming sms.incoming also matched sms.attachment")
	}
	if bus.HasSubscribers(TypeSMSAttachment) {
		t.Error("naming sms.incoming also made sms.attachment observable")
	}
}

// OptInOnly keeps SMS and prompts explicit so unfiltered subscribers cannot
// accidentally arm the phone or receive interactive decisions.
func TestOptInOnlyIncludesConsentEvents(t *testing.T) {
	if len(OptInOnly()) == 0 {
		t.Fatal("OptInOnly() is empty; SMS would reach unfiltered subscribers")
	}
	for _, typ := range OptInOnly() {
		switch typ {
		case TypeSMSIncoming, TypeSMSAttachment, TypePrompt:
		default:
			t.Errorf("OptInOnly() contains %q, which is not an SMS event", typ)
		}
	}
}
