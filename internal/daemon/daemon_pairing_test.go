package daemon

import (
	"testing"

	"github.com/bethropolis/kcd/internal/device"
)

func TestRestoredPairingStateDropsPendingRequests(t *testing.T) {
	tests := []struct {
		name string
		in   device.PairingState
		want device.PairingState
	}{
		{"unknown", device.StateUnknown, device.StateUnpaired},
		{"local request", device.StatePairRequested, device.StateUnpaired},
		{"peer request", device.StatePairRequestedByPeer, device.StateUnpaired},
		{"unpaired", device.StateUnpaired, device.StateUnpaired},
		{"paired", device.StatePaired, device.StatePaired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := restoredPairingState(tt.in); got != tt.want {
				t.Fatalf("restoredPairingState(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
