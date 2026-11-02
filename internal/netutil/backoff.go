// Package netutil contains small helpers for network lifecycle loops.
package netutil

import (
	"context"
	"time"
)

const (
	initialBackoff = 5 * time.Millisecond
	maximumBackoff = time.Second
)

// ErrorBackoff delays repeated network errors without delaying healthy work.
type ErrorBackoff struct {
	delay time.Duration
}

// Reset returns the next error delay to the minimum after successful work.
func (b *ErrorBackoff) Reset() {
	b.delay = initialBackoff
}

// Wait sleeps for the current error delay, doubling it up to the maximum.
// It returns false when the owning context has been canceled.
func (b *ErrorBackoff) Wait(ctx context.Context) bool {
	if b.delay == 0 {
		b.delay = initialBackoff
	}
	timer := time.NewTimer(b.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	if b.delay < maximumBackoff {
		b.delay *= 2
		if b.delay > maximumBackoff {
			b.delay = maximumBackoff
		}
	}
	return true
}
