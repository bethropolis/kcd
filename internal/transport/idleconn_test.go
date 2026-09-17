package transport

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestWithIdleTimeoutDisabled(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if got := WithIdleTimeout(a, 0); got != a {
		t.Error("zero idle must return the conn unwrapped")
	}
	if got := WithIdleTimeout(nil, time.Second); got != nil {
		t.Error("nil conn must stay nil")
	}
	_ = b
}

func TestIdleTimeoutFiresOnSilence(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	r := WithIdleTimeout(a, 50*time.Millisecond)
	buf := make([]byte, 8)
	start := time.Now()
	_, err := r.Read(buf)
	if err == nil {
		t.Fatal("silent read succeeded, want timeout")
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("error = %v, want timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("read blocked %v, want prompt timeout", elapsed)
	}
}

func TestIdleTimeoutExtendedByActivity(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	r := WithIdleTimeout(a, 100*time.Millisecond)
	done := make(chan error, 1)
	go func() {
		// Drip-feed bytes slower in total than the idle window but faster
		// per-gap: total ~150ms of streaming must survive a 100ms idle.
		for i := 0; i < 5; i++ {
			time.Sleep(30 * time.Millisecond)
			if _, err := b.Write([]byte("x")); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	buf := make([]byte, 5)
	for i := 0; i < 5; i++ {
		if _, err := r.Read(buf[i : i+1]); err != nil {
			t.Fatalf("active read %d failed: %v", i, err)
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("writer failed: %v", err)
	}
}
