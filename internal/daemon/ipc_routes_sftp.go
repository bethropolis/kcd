package daemon

import (
	"context"
	"encoding/json"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/sftp"
)

func registerSftpRoutes(handler *ipc.Handler, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdSftpInfo, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		pl, ok := plugins.GetByName("SFTP")
		if !ok {
			return ipc.Response{OK: false, Error: "sftp plugin not enabled"}
		}
		info := pl.(*sftp.SftpPlugin).Info(p.DeviceID)
		if info == nil {
			return ipc.Response{OK: false, Error: "no SFTP credentials cached for this device — use 'kcd sftp request' first"}
		}
		data, _ := json.Marshal(info)
		return ipc.Response{OK: true, Data: data}
	})
	handler.Register(ipc.CmdSftpVolumes, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		pl, ok := plugins.GetByName("SFTP")
		if !ok {
			return ipc.Response{OK: false, Error: "sftp plugin not enabled"}
		}
		volumes := pl.(*sftp.SftpPlugin).Volumes(p.DeviceID)
		if volumes == nil {
			return ipc.Response{OK: false, Error: "no volumes available — use 'kcd sftp request' first"}
		}
		data, _ := json.Marshal(volumes)
		return ipc.Response{OK: true, Data: data}
	})
	handler.Register(ipc.CmdSftpMount, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		pl, ok := plugins.GetByName("SFTP")
		if !ok {
			return ipc.Response{OK: false, Error: "sftp plugin not enabled"}
		}
		dev, ok := devices.Get(p.DeviceID)
		if !ok {
			return ipc.Response{OK: false, Error: "device not found"}
		}
		if err := pl.(*sftp.SftpPlugin).RequestMount(dev); err != nil {
			return ipc.Response{OK: false, Error: err.Error()}
		}
		return ipc.Response{OK: true}
	})
	handler.Register(ipc.CmdSftpMountLocal, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		pl, ok := plugins.GetByName("SFTP")
		if !ok {
			return ipc.Response{OK: false, Error: "sftp plugin not enabled"}
		}
		dev, ok := devices.Get(p.DeviceID)
		if !ok {
			return ipc.Response{OK: false, Error: "device not found"}
		}
		browsePath, err := pl.(*sftp.SftpPlugin).RequestAndMount(context.Background(), dev)
		if err != nil {
			return ipc.Response{OK: false, Error: err.Error()}
		}
		data, _ := json.Marshal(map[string]string{"path": browsePath})
		return ipc.Response{OK: true, Data: data}
	})
	handler.Register(ipc.CmdSftpUnmount, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		pl, ok := plugins.GetByName("SFTP")
		if !ok {
			return ipc.Response{OK: false, Error: "sftp plugin not enabled"}
		}
		if err := pl.(*sftp.SftpPlugin).Unmount(p.DeviceID); err != nil {
			return ipc.Response{OK: false, Error: err.Error()}
		}
		return ipc.Response{OK: true}
	})
}
