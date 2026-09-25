package daemon

import (
	"context"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/runcommand"
	"github.com/bethropolis/kcd/internal/protocol"
)

func registerRunCommandRoutes(handler *ipc.Handler, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdRunList, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		// The list only exists on the phone, so this blocks until it
		// answers or the plugin's own deadline expires — same shape as
		// the sftp mount route.
		return deviceRoute(req, &p, devices, plugins, "RunCommand", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
			commands, err := pl.(*runcommand.RunCommandPlugin).RequestList(context.Background(), dev)
			if err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			if commands == nil {
				commands = []runcommand.Command{}
			}
			return jsonOK(commands)
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
