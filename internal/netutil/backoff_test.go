package netutil

import (
	"context"
	"testing"
	"time"
)

func TestErrorBackoffIncreasesAndResets(t *testing.T) {
	ctx := context.Background()
	backoff := ErrorBackoff{}
	start := time.Now()
	if !backoff.Wait(ctx) {
		t.Fatal("first wait canceled")
	}
	first := time.Since(start)
	if first < initialBackoff {
		t.Fatalf("first delay %s shorter than %s", first, initialBackoff)
	}
	if backoff.delay != 2*initialBackoff {
		t.Fatalf("next delay %s, want %s", backoff.delay, 2*initialBackoff)
	}
	backoff.Reset()
	if backoff.delay != initialBackoff {
		t.Fatalf("reset delay %s, want %s", backoff.delay, initialBackoff)
	}
}

func TestErrorBackoffStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if (&ErrorBackoff{}).Wait(ctx) {
		t.Fatal("wait succeeded after cancellation")
	}
}
