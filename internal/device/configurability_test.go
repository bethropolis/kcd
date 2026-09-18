package device

import (
	"github.com/bethropolis/kcd/internal/log"
	"testing"
	"time"
)

func TestConfiguredReconnectBackoff(t *testing.T) {
	for _, tc := range []struct {
		attempt            int
		initial, cap, want time.Duration
	}{
		{0, time.Second, 5 * time.Second, time.Second},
		{2, time.Second, 5 * time.Second, 4 * time.Second},
		{3, time.Second, 5 * time.Second, 5 * time.Second},
		{1000000, time.Nanosecond, time.Duration(1<<63 - 1), time.Duration(1<<63 - 1)},
		{1, time.Duration(1 << 62), time.Duration(1<<63 - 1), time.Duration(1<<63 - 1)},
	} {
		if got := ReconnectBackoff(tc.attempt, tc.cap, tc.initial); got != tc.want {
			t.Errorf("%+v: got %v", tc, got)
		}
	}
}

func TestConfiguredPairIntentTTL(t *testing.T) {
	dev := NewDevice("peer", "Peer", "phone", log.Nop())
	before := time.Now()
	dev.RequestPairDial(time.Hour)
	deadline := time.Unix(0, dev.pairIntentUntil.Load())
	if deadline.Before(before.Add(time.Hour)) || deadline.After(time.Now().Add(time.Hour)) {
		t.Fatalf("unexpected intent deadline %s", deadline)
	}
	dev.ClearPairDial()
	if dev.PairDialActive() {
		t.Fatal("cleared intent still active")
	}
}
