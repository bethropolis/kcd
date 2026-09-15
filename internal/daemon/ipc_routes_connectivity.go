package daemon

import (
	"encoding/json"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/connectivity"
)

func registerConnectivityRoutes(handler *ipc.Handler, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdConnectivity, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		dev, ok := devices.Get(p.DeviceID)
		if !ok {
			return ipc.Response{OK: false, Error: "device not found"}
		}
		pl, ok := plugins.GetByName("Connectivity")
		if !ok {
			return ipc.Response{OK: false, Error: "connectivity plugin not enabled"}
		}
		report, ok := pl.(*connectivity.ConnectivityPlugin).Report(dev.ID())
		if !ok {
			return ipc.Response{OK: false, Error: "no connectivity data (device offline or never reported)"}
		}
		data, _ := json.Marshal(report)
		return ipc.Response{OK: true, Data: data}
	})
}
