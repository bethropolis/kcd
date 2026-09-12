package daemon

import (
	"testing"

	"github.com/bethropolis/kcd/internal/device"
	"go.uber.org/zap"
)

func ephemeralDevice(state device.PairingState, markEphemeral, pairIntent bool) *device.Device {
	dev := device.NewDevice("test-id", "Test", "phone", zap.NewNop())
	dev.SetState(state)
	if markEphemeral {
		dev.MarkEphemeralDialed()
	}
	if pairIntent {
		dev.RequestPairDial()
	}
	return dev
}

func TestShouldEphemeralClose(t *testing.T) {
	cases := []struct {
		name        string
		dev         *device.Device
		pairingMode bool
		want        bool
	}{
		{"pairing mode keeps", ephemeralDevice(device.StateUnpaired, true, false), true, false},
		{"never dialled keeps", ephemeralDevice(device.StateUnpaired, false, false), false, false},
		{"paired keeps", ephemeralDevice(device.StatePaired, true, false), false, false},
		{"pair requested keeps", ephemeralDevice(device.StatePairRequested, true, false), false, false},
		{"pair requested by peer keeps", ephemeralDevice(device.StatePairRequestedByPeer, true, false), false, false},
		{"explicit intent keeps", ephemeralDevice(device.StateUnpaired, true, true), false, false},
		{"idle stranger closes", ephemeralDevice(device.StateUnpaired, true, false), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldEphemeralClose(tc.dev, tc.pairingMode); got != tc.want {
				t.Errorf("shouldEphemeralClose() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEphemeralMarkerReset(t *testing.T) {
	dev := device.NewDevice("test-id", "Test", "phone", zap.NewNop())
	if dev.EphemeralDialed() {
		t.Fatal("new device must not be ephemeral-marked")
	}
	dev.MarkEphemeralDialed()
	if !dev.EphemeralDialed() {
		t.Fatal("marker not set")
	}
	dev.ClearEphemeral()
	if dev.EphemeralDialed() {
		t.Fatal("marker not cleared")
	}
	if dev.PairDialPending() {
		t.Fatal("new device must have no pair intent")
	}
	dev.RequestPairDial()
	if !dev.PairDialPending() {
		t.Fatal("pair intent not visible")
	}
	if !dev.ConsumePairDial() {
		t.Fatal("pair intent not consumed")
	}
	if dev.PairDialPending() {
		t.Fatal("pair intent not cleared after consume")
	}
}
