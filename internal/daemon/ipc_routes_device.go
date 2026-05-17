package daemon

import (
	"encoding/json"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/findmyphone"
	"github.com/bethropolis/kcd/internal/plugins/lockdevice"
)

func registerDeviceRoutes(handler *ipc.Handler, cfg *config.Config, devices *device.Registry, plugins *plugin.Registry) {
	if cfg.Plugins.FindMyPhone {
		handler.Register(ipc.CmdFindMyPhone, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			pl, ok := plugins.GetByName("FindMyPhone")
			if !ok {
				return ipc.Response{OK: false, Error: "findmyphone plugin not enabled"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			if err := pl.(*findmyphone.FindMyPhonePlugin).Ring(dev); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
	}
	if cfg.Plugins.LockDevice {
		handler.Register(ipc.CmdLock, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			pl, ok := plugins.GetByName("LockDevice")
			if !ok {
				return ipc.Response{OK: false, Error: "lockdevice plugin not enabled"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			if err := pl.(*lockdevice.LockDevicePlugin).Lock(dev); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
		handler.Register(ipc.CmdUnlock, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			pl, ok := plugins.GetByName("LockDevice")
			if !ok {
				return ipc.Response{OK: false, Error: "lockdevice plugin not enabled"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			if err := pl.(*lockdevice.LockDevicePlugin).Unlock(dev); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
	}
}
