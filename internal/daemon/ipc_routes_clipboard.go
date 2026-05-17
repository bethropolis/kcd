package daemon

import (
	"context"
	"encoding/json"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/clipboard"
)

func registerClipboardRoutes(handler *ipc.Handler, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdClipboardPush, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		dev, ok := devices.Get(p.DeviceID)
		if !ok {
			return ipc.Response{OK: false, Error: "device not found"}
		}
		pl, ok := plugins.GetByName("Clipboard")
		if !ok {
			return ipc.Response{OK: false, Error: "clipboard plugin not enabled"}
		}
		if err := clipboard.Push(context.Background(), dev, pl.(*clipboard.ClipboardPlugin)); err != nil {
			return ipc.Response{OK: false, Error: "clipboard push failed: " + err.Error()}
		}
		return ipc.Response{OK: true}
	})
}
