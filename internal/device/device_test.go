package device

import (
	"testing"
	"time"

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
