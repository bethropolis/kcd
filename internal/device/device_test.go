package device

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/transport"
	"go.uber.org/zap/zaptest"
)

func TestRegistry_Deduplicate(t *testing.T) {
	reg := NewRegistry(nil)
	logger := zaptest.NewLogger(t)

	d1 := NewDevice("123", "Phone 1", "phone", logger)
	d2 := NewDevice("123", "Phone 2", "phone", logger)

	reg.Add(d1)
	reg.Add(d2)

	devices := reg.List()
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}

	if devices[0].Name() != "Phone 2" {
		t.Errorf("expected updated name 'Phone 2', got %q", devices[0].Name())
	}
}

func TestReconnectBackoff(t *testing.T) {
	max := 60 * time.Second
	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 2 * time.Second},
		{1, 4 * time.Second},
		{2, 8 * time.Second},
		{3, 16 * time.Second},
		{5, 60 * time.Second}, // Caps out
		{10, 60 * time.Second},
	}

	for _, tt := range tests {
		actual := ReconnectBackoff(tt.attempt, max)
		if actual != tt.expected {
			t.Errorf("attempt %d: expected %v, got %v", tt.attempt, tt.expected, actual)
		}
	}
}

func TestDevice_ReconnectAttempt(t *testing.T) {
	logger := zaptest.NewLogger(t)
	d := NewDevice("123", "Phone", "phone", logger)

	if got := d.ReconnectAttempt(); got != 0 {
		t.Fatalf("expected initial attempt 0, got %d", got)
	}

	d.SetReconnectAttempt(3)
	if got := d.ReconnectAttempt(); got != 3 {
		t.Fatalf("expected attempt 3, got %d", got)
	}

	d.ResetReconnectAttempt()
	if got := d.ReconnectAttempt(); got != 0 {
		t.Fatalf("expected attempt 0 after reset, got %d", got)
	}
}

func TestDevice_ConnectionAge(t *testing.T) {
	logger := zaptest.NewLogger(t)
	d := NewDevice("123", "Phone", "phone", logger)

	if got := d.ConnectionAge(); got != 0 {
		t.Fatalf("expected 0 for never-connected device, got %v", got)
	}

	// Connect with a pipe-based TLS connection (no handshake required).
	left, right := net.Pipe()
	conn := transport.NewConn(tls.Client(left, &tls.Config{InsecureSkipVerify: true}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.Connect(ctx, conn, nil, nil, nil)

	if got := d.ConnectionAge(); got <= 0 {
		t.Fatalf("expected positive age after connect, got %v", got)
	}

	time.Sleep(20 * time.Millisecond)
	if got := d.ConnectionAge(); got < 20*time.Millisecond {
		t.Errorf("expected age to grow past 20ms, got %v", got)
	}

	// Tear down: closing the pipe unblocks readLoop, which clears the
	// connection. Wait for it so no goroutine logs after the test returns.
	_ = right.Close()
	deadline := time.Now().Add(2 * time.Second)
	for d.IsConnected() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	d.Disconnect()
}
