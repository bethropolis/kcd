package daemon

import (
	"context"
	"encoding/json"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/share"
)

func registerShareRoutes(handler *ipc.Handler, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdShare, func(req ipc.Request) ipc.Response {
		var p ipc.SharePayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		dev, ok := devices.Get(p.DeviceID)
		if !ok {
			return ipc.Response{OK: false, Error: "device not found"}
		}
		pl, ok := plugins.GetByName("Share")
		if !ok {
			return ipc.Response{OK: false, Error: "share plugin not enabled"}
		}
		sharePl, ok := pl.(*share.SharePlugin)
		if !ok {
			return ipc.Response{OK: false, Error: "invalid share plugin type"}
		}
		if err := sharePl.SendFile(context.Background(), dev, p.FilePath); err != nil {
			return ipc.Response{OK: false, Error: "share failed: " + err.Error()}
		}
		return ipc.Response{OK: true}
	})
}
