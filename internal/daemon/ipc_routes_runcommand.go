package daemon

import (
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
)

func registerRunCommandRoutes(handler *ipc.Handler, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdRunList, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		return deviceRoute(req, &p, devices, plugins, "", func(dev *device.Device, _ plugin.Plugin) ipc.Response {
			pkt, _ := protocol.NewPacket("kdeconnect.runcommand.request", map[string]bool{"requestCommandList": true})
			if err := dev.Send(pkt); err != nil {
				return ipc.Response{OK: false, Error: "failed to send runcommand list request"}
			}
			return ipc.Response{OK: true}
		})
	})
	handler.Register(ipc.CmdRunExec, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		return deviceRoute(req, &p, devices, plugins, "", func(dev *device.Device, _ plugin.Plugin) ipc.Response {
			pkt, _ := protocol.NewPacket("kdeconnect.runcommand.request", map[string]string{"key": p.Key})
			if err := dev.Send(pkt); err != nil {
				return ipc.Response{OK: false, Error: "failed to send runcommand exec request"}
			}
			return ipc.Response{OK: true}
		})
	})
}
