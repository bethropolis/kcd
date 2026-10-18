package ipc

import (
	"encoding/json"
	"testing"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugins/pair"
)

func TestPairRejectDeclinesPeerRequest(t *testing.T) {
	logger := log.Nop()
	bus := events.NewBus(logger)
	devices := device.NewRegistry(bus)
	cfg := config.Defaults()
	pl := pair.NewPairPlugin(devices, nil, cfg.Pairing, nil, bus, logger)
	h := NewHandler(devices, nil, pl, nil, bus, 0)

	dev := device.NewDevice("phone1", "Phone", "phone", logger)
	dev.SetState(device.StatePairRequestedByPeer)
	devices.Add(dev)

	payload, _ := json.Marshal(PairPayload{DeviceID: "phone1", Reject: true})
	if resp := h.handlePair(payload); !resp.OK {
		t.Fatalf("reject failed: %+v", resp)
	}
	if got := dev.State(); got == device.StatePaired {
		t.Fatalf("reject paired the device (state %v)", got)
	}
}
