package daemon

import (
	"context"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/clipboard"
)

func registerClipboardRoutes(handler *ipc.Handler, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdClipboardPush, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		return deviceRoute(req, &p, devices, plugins, "Clipboard", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
			if err := clipboard.Push(context.Background(), dev, pl.(*clipboard.ClipboardPlugin)); err != nil {
				return ipc.Response{OK: false, Error: "clipboard push failed: " + err.Error()}
			}
			return ipc.Response{OK: true}
		})
	})
}
