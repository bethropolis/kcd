package main

import (
	"testing"
	"time"
)

func TestPairListenDeadline(t *testing.T) {
	for _, tc := range []struct{ input, want time.Duration }{
		{time.Minute, 70 * time.Second},
		{3 * time.Minute, 190 * time.Second},
		{time.Duration(1<<63 - 1), time.Duration(1<<63 - 1)},
	} {
		if got := pairListenDeadline(tc.input); got != tc.want {
			t.Errorf("%v: got %v, want %v", tc.input, got, tc.want)
		}
	}
}
