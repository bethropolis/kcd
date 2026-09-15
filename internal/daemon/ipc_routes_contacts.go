package daemon

import (
	"encoding/json"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/contacts"
)

func registerContactsRoutes(handler *ipc.Handler, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdContactsSync, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		pl, ok := plugins.GetByName("Contacts")
		if !ok {
			return ipc.Response{OK: false, Error: "contacts plugin not enabled"}
		}
		dev, ok := devices.Get(p.DeviceID)
		if !ok {
			return ipc.Response{OK: false, Error: "device not found"}
		}
		if !dev.IsConnected() {
			return ipc.Response{OK: false, Error: "device not connected"}
		}
		if err := pl.(*contacts.ContactsPlugin).RequestSync(dev); err != nil {
			return ipc.Response{OK: false, Error: err.Error()}
		}
		return ipc.Response{OK: true}
	})

	handler.Register(ipc.CmdContactsList, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		pl, ok := plugins.GetByName("Contacts")
		if !ok {
			return ipc.Response{OK: false, Error: "contacts plugin not enabled"}
		}
		list := pl.(*contacts.ContactsPlugin).List(p.DeviceID)
		if list == nil {
			list = []contacts.ContactSummary{}
		}
		data, _ := json.Marshal(list)
		return ipc.Response{OK: true, Data: data}
	})
}
