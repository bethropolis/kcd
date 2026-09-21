package daemon

import (
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
			return deviceRoute(req, &p, devices, plugins, "FindMyPhone", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
				if err := pl.(*findmyphone.FindMyPhonePlugin).Ring(dev); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})
	}
	if cfg.Plugins.LockDevice {
		handler.Register(ipc.CmdLock, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			return deviceRoute(req, &p, devices, plugins, "LockDevice", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
				if err := pl.(*lockdevice.LockDevicePlugin).Lock(dev); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})
		handler.Register(ipc.CmdUnlock, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			return deviceRoute(req, &p, devices, plugins, "LockDevice", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
				if err := pl.(*lockdevice.LockDevicePlugin).Unlock(dev); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})
	}
}
