package daemon

import (
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/contacts"
)

func registerContactsRoutes(handler *ipc.Handler, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdContactsSync, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		return deviceRoute(req, &p, devices, plugins, "Contacts", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
			if !dev.IsConnected() {
				return ipc.Response{OK: false, Error: "device not connected"}
			}
			if err := pl.(*contacts.ContactsPlugin).RequestSync(dev); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
	})

	handler.Register(ipc.CmdContactsList, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		return pluginRoute(req, &p, plugins, "Contacts", func(pl plugin.Plugin) ipc.Response {
			list := pl.(*contacts.ContactsPlugin).List(p.DeviceID)
			if list == nil {
				list = []contacts.ContactSummary{}
			}
			return jsonOK(list)
		})
	})

	handler.Register(ipc.CmdContactsClear, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		return pluginRoute(req, &p, plugins, "Contacts", func(pl plugin.Plugin) ipc.Response {
			if err := pl.(*contacts.ContactsPlugin).ForgetDevice(p.DeviceID); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
	})
}
