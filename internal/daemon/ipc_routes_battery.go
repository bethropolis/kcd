package daemon

import (
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
)

func registerBatteryRoutes(handler *ipc.Handler, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdBattery, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		return deviceRoute(req, &p, devices, plugins, "", func(dev *device.Device, _ plugin.Plugin) ipc.Response {
			// Fail closed when no packet was ever received: returning the
			// zero values would be indistinguishable from a real 0% reading.
			if !dev.HasBattery() {
				return ipc.Response{OK: false, Error: "no battery reading yet"}
			}
			charge, charging := dev.GetBattery()
			return jsonOK(map[string]interface{}{
				"charge":       charge,
				"charging":     charging,
				"batteryAgeMs": dev.BatteryAge().Milliseconds(),
			})
		})
	})
}
