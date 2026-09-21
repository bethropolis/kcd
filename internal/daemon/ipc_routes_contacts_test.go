package daemon

import (
	"encoding/json"
	"testing"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/contacts"
)

func contactsTestHandler(t *testing.T, withPlugin bool) *ipc.Handler {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	bus := events.NewBus(log.Nop())
	devices := device.NewRegistry(bus)
	plugins := plugin.NewRegistry(log.Nop())
	if withPlugin {
		plugins.Register(contacts.NewContactsPlugin(nil, log.Nop()))
	}
	h := ipc.NewHandler(devices, plugins, nil, "", bus, 0)
	registerContactsRoutes(h, devices, plugins)
	return h
}

func contactsRequest(cmd, deviceID string) ipc.Request {
	payload, _ := json.Marshal(ipc.DevicePayload{DeviceID: deviceID})
	return ipc.Request{Command: cmd, Payload: payload}
}

// Clear is idempotent and offline-capable: unknown device, no connection.
func TestContactsClearRoute(t *testing.T) {
	h := contactsTestHandler(t, true)
	resp := h.HandleRequest(contactsRequest(ipc.CmdContactsClear, "ghost"))
	if !resp.OK {
		t.Fatalf("clear unknown device failed: %q", resp.Error)
	}
}

// Disabled plugin surfaces the registry-derived error.
func TestContactsClearPluginDisabled(t *testing.T) {
	h := contactsTestHandler(t, false)
	resp := h.HandleRequest(contactsRequest(ipc.CmdContactsClear, "ghost"))
	if resp.Error != "contacts plugin not enabled" {
		t.Fatalf("error = %q, want contacts plugin not enabled", resp.Error)
	}
}

// Malformed payload is rejected before touching the plugin.
func TestContactsClearInvalidPayload(t *testing.T) {
	h := contactsTestHandler(t, true)
	resp := h.HandleRequest(ipc.Request{Command: ipc.CmdContactsClear, Payload: json.RawMessage(`{bad`)})
	if resp.Error != "invalid payload" {
		t.Fatalf("error = %q, want invalid payload", resp.Error)
	}
}
