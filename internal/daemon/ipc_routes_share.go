package daemon

import (
	"context"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/share"
)

func registerShareRoutes(handler *ipc.Handler, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdShare, func(req ipc.Request) ipc.Response {
		var p ipc.SharePayload
		return deviceRoute(req, &p, devices, plugins, "Share", func(dev *device.Device, sharePl *share.SharePlugin) ipc.Response {
			if err := sharePl.SendFile(context.Background(), dev, p.FilePath); err != nil {
				return ipc.Response{OK: false, Error: "share failed: " + err.Error()}
			}
			return ipc.Response{OK: true}
		})
	})
}
