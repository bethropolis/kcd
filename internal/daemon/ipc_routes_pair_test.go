package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/pair"
)

func TestConfiguredPairListenTimeout(t *testing.T) {
	logger := log.Nop()
	bus := events.NewBus(logger)
	devices := device.NewRegistry(bus)
	plugins := plugin.NewRegistry(logger)
	cfg := config.Defaults()
	cfg.Pairing.ListenTimeout = "1ms"
	pl := pair.NewPairPlugin(devices, nil, cfg.Pairing, nil, bus, logger)
	plugins.Register(pl)

	h := ipc.NewHandler(devices, plugins, bus)
	registerPairRoutes(h, cfg, devices, plugins, bus, nil, 0, nil)

	done := make(chan ipc.Response, 1)
	go func() {
		done <- h.HandleRequest(ipc.Request{Command: ipc.CmdPairListen})
	}()

	select {
	case resp := <-done:
		if resp.OK || !strings.Contains(resp.Error, "(1ms)") {
			t.Fatalf("unexpected response: %+v", resp)
		}
	case <-time.After(time.Second):
		t.Fatal("configured timeout not applied")
	}
}

func TestPairRejectDeclinesPeerRequest(t *testing.T) {
	logger := log.Nop()
	bus := events.NewBus(logger)
	devices := device.NewRegistry(bus)
	plugins := plugin.NewRegistry(logger)
	cfg := config.Defaults()
	pl := pair.NewPairPlugin(devices, nil, cfg.Pairing, nil, bus, logger)
	plugins.Register(pl)

	h := ipc.NewHandler(devices, plugins, bus)
	registerPairRoutes(h, cfg, devices, plugins, bus, nil, 0, nil)

	dev := device.NewDevice("phone1", "Phone", "phone", logger)
	dev.SetState(device.StatePairRequestedByPeer)
	devices.Add(dev)

	payload, _ := json.Marshal(ipc.PairPayload{DeviceID: "phone1", Reject: true})
	resp := h.HandleRequest(ipc.Request{Command: ipc.CmdPair, Payload: payload})
	if !resp.OK {
		t.Fatalf("reject failed: %+v", resp)
	}
	if got := dev.State(); got == device.StatePaired {
		t.Fatalf("reject paired the device (state %v)", got)
	}
}
