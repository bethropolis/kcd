package daemon

import (
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/connectivity"
)

func registerConnectivityRoutes(handler *ipc.Handler, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdConnectivity, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		return deviceRoute(req, &p, devices, plugins, "Connectivity", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
			report, ok := pl.(*connectivity.ConnectivityPlugin).Report(dev.ID())
			if !ok {
				return ipc.Response{OK: false, Error: "no connectivity data (device offline or never reported)"}
			}
			return jsonOK(report)
		})
	})
}
