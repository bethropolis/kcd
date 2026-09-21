package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
)

type stubRoutePlugin struct{ name string }

func (s *stubRoutePlugin) Name() string               { return s.name }
func (s *stubRoutePlugin) IncomingTypes() []string    { return nil }
func (s *stubRoutePlugin) OutgoingTypes() []string    { return nil }
func (s *stubRoutePlugin) Timeout() time.Duration     { return 0 }
func (s *stubRoutePlugin) OnConnect(device.Sender)    {}
func (s *stubRoutePlugin) OnDisconnect(device.Sender) {}
func (s *stubRoutePlugin) Handle(context.Context, device.Sender, *protocol.Packet) error {
	return nil
}

func routeTestRegistries(t *testing.T) (*device.Registry, *plugin.Registry) {
	t.Helper()
	bus := events.NewBus(log.Nop())
	devices := device.NewRegistry(bus)
	devices.Add(device.NewDevice("dev1", "Test", "phone", log.Nop()))
	plugins := plugin.NewRegistry(log.Nop())
	plugins.Register(&stubRoutePlugin{name: "SMS"})
	return devices, plugins
}

func TestDeviceRoutePreamble(t *testing.T) {
	devices, plugins := routeTestRegistries(t)
	okAction := func(dev *device.Device, pl plugin.Plugin) ipc.Response {
		if dev.ID() != "dev1" {
			t.Errorf("action got device %q, want dev1", dev.ID())
		}
		if pl == nil || pl.Name() != "SMS" {
			t.Errorf("action got plugin %v, want SMS", pl)
		}
		return ipc.Response{OK: true}
	}

	payload := json.RawMessage(`{"deviceId":"dev1"}`)
	if resp := deviceRoute(ipc.Request{Payload: payload}, &ipc.DevicePayload{}, devices, plugins, "SMS", okAction); !resp.OK {
		t.Errorf("happy path failed: %q", resp.Error)
	}

	if resp := deviceRoute(ipc.Request{Payload: json.RawMessage(`{bad`)}, &ipc.DevicePayload{}, devices, plugins, "SMS", okAction); resp.Error != "invalid payload" {
		t.Errorf("malformed payload error = %q, want invalid payload", resp.Error)
	}

	if resp := deviceRoute(ipc.Request{Payload: payload}, &ipc.DevicePayload{}, devices, plugins, "Missing", okAction); resp.Error != "missing plugin not enabled" {
		t.Errorf("unknown plugin error = %q, want missing plugin not enabled", resp.Error)
	}

	missing := json.RawMessage(`{"deviceId":"nope"}`)
	if resp := deviceRoute(ipc.Request{Payload: missing}, &ipc.DevicePayload{}, devices, plugins, "SMS", okAction); resp.Error != "device not found" {
		t.Errorf("unknown device error = %q, want device not found", resp.Error)
	}

	// Empty plugin name skips the lookup for device-only routes.
	seenNil := false
	if resp := deviceRoute(ipc.Request{Payload: payload}, &ipc.DevicePayload{}, devices, plugins, "", func(_ *device.Device, pl plugin.Plugin) ipc.Response {
		seenNil = pl == nil
		return ipc.Response{OK: true}
	}); !resp.OK || !seenNil {
		t.Errorf("empty plugin name: ok=%v seenNil=%v, want ok with nil plugin", resp.OK, seenNil)
	}
}

func TestPluginRoutePreamble(t *testing.T) {
	_, plugins := routeTestRegistries(t)
	payload := json.RawMessage(`{"deviceId":"dev1"}`)
	okAction := func(pl plugin.Plugin) ipc.Response {
		if pl.Name() != "SMS" {
			t.Errorf("action got plugin %v, want SMS", pl)
		}
		return ipc.Response{OK: true}
	}

	if resp := pluginRoute(ipc.Request{Payload: payload}, &ipc.DevicePayload{}, plugins, "SMS", okAction); !resp.OK {
		t.Errorf("happy path failed: %q", resp.Error)
	}
	if resp := pluginRoute(ipc.Request{Payload: json.RawMessage(`{bad`)}, &ipc.DevicePayload{}, plugins, "SMS", okAction); resp.Error != "invalid payload" {
		t.Errorf("malformed payload error = %q, want invalid payload", resp.Error)
	}
	if resp := pluginRoute(ipc.Request{Payload: payload}, &ipc.DevicePayload{}, plugins, "Missing", okAction); resp.Error != "missing plugin not enabled" {
		t.Errorf("unknown plugin error = %q, want missing plugin not enabled", resp.Error)
	}
}

func TestJsonOK(t *testing.T) {
	resp := jsonOK(map[string]int{"charge": 85})
	if !resp.OK {
		t.Fatal("jsonOK must succeed")
	}
	var got map[string]int
	if err := json.Unmarshal(resp.Data, &got); err != nil || got["charge"] != 85 {
		t.Errorf("jsonOK round-trip = %v, %v; want charge 85", got, err)
	}
}
