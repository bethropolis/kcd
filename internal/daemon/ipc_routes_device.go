package daemon

import (
	"encoding/json"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/findmyphone"
	"github.com/bethropolis/kcd/internal/plugins/lockdevice"
	"github.com/bethropolis/kcd/internal/plugins/ping"
)

func registerDeviceRoutes(handler *ipc.Handler, cfg *config.Config, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdDevices, func(req ipc.Request) ipc.Response {
		devs := devices.List()
		infos := make([]ipc.DeviceSummary, 0, len(devs))
		for _, dev := range devs {
			infos = append(infos, ipc.SummarizeDevice(dev, plugins))
		}
		data, err := json.Marshal(infos)
		if err != nil {
			return ipc.Response{OK: false, Error: "failed to marshal device list"}
		}
		return ipc.Response{OK: true, Data: data}
	})

	handler.Register(ipc.CmdPing, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		return deviceRoute(req, &p, devices, plugins, "Ping", func(dev *device.Device, pingPl *ping.PingPlugin) ipc.Response {
			if err := pingPl.Ping(dev); err != nil {
				return ipc.Response{OK: false, Error: "failed to send ping packet: " + err.Error()}
			}
			return ipc.Response{OK: true}
		})
	})
	if cfg.Plugins.FindMyPhone {
		handler.Register(ipc.CmdFindMyPhone, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			return deviceRoute(req, &p, devices, plugins, "FindMyPhone", func(dev *device.Device, fmp *findmyphone.FindMyPhonePlugin) ipc.Response {
				if err := fmp.Ring(dev); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})
	}
	if cfg.Plugins.LockDevice {
		handler.Register(ipc.CmdLock, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			return deviceRoute(req, &p, devices, plugins, "LockDevice", func(dev *device.Device, ld *lockdevice.LockDevicePlugin) ipc.Response {
				if err := ld.Lock(dev); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})
		handler.Register(ipc.CmdUnlock, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			return deviceRoute(req, &p, devices, plugins, "LockDevice", func(dev *device.Device, ld *lockdevice.LockDevicePlugin) ipc.Response {
				if err := ld.Unlock(dev); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})
	}
}
