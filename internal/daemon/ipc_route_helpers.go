package daemon

import (
	"encoding/json"
	"strings"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
)

// hasDeviceID is implemented by IPC payloads that address a device.
// The methods live on the ipc payload types (see ipc/payload.go).
type hasDeviceID interface {
	GetDeviceID() string
}

// deviceRoute handles the standard decode → plugin → device preamble
// shared by most IPC handlers. An empty pluginName skips the plugin
// lookup (pl is nil) for device-only routes. Error strings match
// docs/IPC_PROTOCOL.md byte-for-byte; the disabled-plugin message is
// derived from the registry name, which keeps every route consistent.
func deviceRoute[P hasDeviceID](req ipc.Request, payload P, devices *device.Registry, plugins *plugin.Registry, pluginName string, action func(dev *device.Device, pl plugin.Plugin) ipc.Response) ipc.Response {
	if err := json.Unmarshal(req.Payload, payload); err != nil {
		return ipc.Response{OK: false, Error: "invalid payload"}
	}
	var pl plugin.Plugin
	if pluginName != "" {
		var ok bool
		if pl, ok = plugins.GetByName(pluginName); !ok {
			return ipc.Response{OK: false, Error: strings.ToLower(pluginName) + " plugin not enabled"}
		}
	}
	dev, ok := devices.Get(payload.GetDeviceID())
	if !ok {
		return ipc.Response{OK: false, Error: "device not found"}
	}
	return action(dev, pl)
}

// pluginRoute handles the decode → plugin preamble for routes that do
// not address a single device (listings, global status).
func pluginRoute[P any](req ipc.Request, payload P, plugins *plugin.Registry, pluginName string, action func(pl plugin.Plugin) ipc.Response) ipc.Response {
	if err := json.Unmarshal(req.Payload, payload); err != nil {
		return ipc.Response{OK: false, Error: "invalid payload"}
	}
	pl, ok := plugins.GetByName(pluginName)
	if !ok {
		return ipc.Response{OK: false, Error: strings.ToLower(pluginName) + " plugin not enabled"}
	}
	return action(pl)
}

// jsonOK marshals v into a successful IPC response. Marshal failures
// are ignored, matching the previous inline behavior at every site.
func jsonOK(v any) ipc.Response {
	data, _ := json.Marshal(v)
	return ipc.Response{OK: true, Data: data}
}
