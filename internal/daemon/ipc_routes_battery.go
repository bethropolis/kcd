package daemon

import (
	"encoding/json"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
)

func registerBatteryRoutes(handler *ipc.Handler, devices *device.Registry) {
	handler.Register(ipc.CmdBattery, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		dev, ok := devices.Get(p.DeviceID)
		if !ok {
			return ipc.Response{OK: false, Error: "device not found"}
		}
		charge, charging := dev.GetBattery()
		data, _ := json.Marshal(map[string]interface{}{
			"charge":   charge,
			"charging": charging,
		})
		return ipc.Response{OK: true, Data: data}
	})
}
