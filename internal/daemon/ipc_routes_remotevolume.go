package daemon

import (
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/remotesystemvolume"
)

func registerRemoteVolumeRoutes(handler *ipc.Handler, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdRemoteVolumeList, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		return pluginRoute(req, &p, plugins, "RemoteSystemVolume", func(rsv *remotesystemvolume.RemoteSystemVolumePlugin) ipc.Response {
			sinks := rsv.ListSinks(p.DeviceID)
			if sinks == nil {
				return jsonOK([]remotesystemvolume.SinkInfo{})
			}
			return jsonOK(sinks)
		})
	})

	handler.Register(ipc.CmdRemoteVolumeSet, func(req ipc.Request) ipc.Response {
		var p ipc.RemoteVolumeSetPayload
		return deviceRoute(req, &p, devices, plugins, "RemoteSystemVolume", func(dev *device.Device, rsv *remotesystemvolume.RemoteSystemVolumePlugin) ipc.Response {
			if err := rsv.SetVolume(dev, p.Name, p.Volume); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
	})

	handler.Register(ipc.CmdRemoteVolumeMute, func(req ipc.Request) ipc.Response {
		var p ipc.RemoteVolumeMutePayload
		return deviceRoute(req, &p, devices, plugins, "RemoteSystemVolume", func(dev *device.Device, rsv *remotesystemvolume.RemoteSystemVolumePlugin) ipc.Response {
			if err := rsv.SetMuted(dev, p.Name, p.Muted); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
	})
}
