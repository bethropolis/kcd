package daemon

import (
	"encoding/json"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/remotesystemvolume"
)

func registerRemoteVolumeRoutes(handler *ipc.Handler, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdRemoteVolumeList, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		pl, ok := plugins.GetByName("RemoteSystemVolume")
		if !ok {
			return ipc.Response{OK: false, Error: "remotesystemvolume plugin not enabled"}
		}
		sinks := pl.(*remotesystemvolume.RemoteSystemVolumePlugin).ListSinks(p.DeviceID)
		if sinks == nil {
			return ipc.Response{OK: true, Data: mustJSON([]remotesystemvolume.SinkInfo{})}
		}
		data, _ := json.Marshal(sinks)
		return ipc.Response{OK: true, Data: data}
	})

	handler.Register(ipc.CmdRemoteVolumeSet, func(req ipc.Request) ipc.Response {
		var p struct {
			DeviceID string `json:"deviceId"`
			Name     string `json:"name"`
			Volume   int    `json:"volume"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		pl, ok := plugins.GetByName("RemoteSystemVolume")
		if !ok {
			return ipc.Response{OK: false, Error: "remotesystemvolume plugin not enabled"}
		}
		dev, ok := devices.Get(p.DeviceID)
		if !ok {
			return ipc.Response{OK: false, Error: "device not found"}
		}
		if err := pl.(*remotesystemvolume.RemoteSystemVolumePlugin).SetVolume(dev, p.Name, p.Volume); err != nil {
			return ipc.Response{OK: false, Error: err.Error()}
		}
		return ipc.Response{OK: true}
	})

	handler.Register(ipc.CmdRemoteVolumeMute, func(req ipc.Request) ipc.Response {
		var p struct {
			DeviceID string `json:"deviceId"`
			Name     string `json:"name"`
			Muted    bool   `json:"muted"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		pl, ok := plugins.GetByName("RemoteSystemVolume")
		if !ok {
			return ipc.Response{OK: false, Error: "remotesystemvolume plugin not enabled"}
		}
		dev, ok := devices.Get(p.DeviceID)
		if !ok {
			return ipc.Response{OK: false, Error: "device not found"}
		}
		if err := pl.(*remotesystemvolume.RemoteSystemVolumePlugin).SetMuted(dev, p.Name, p.Muted); err != nil {
			return ipc.Response{OK: false, Error: err.Error()}
		}
		return ipc.Response{OK: true}
	})
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
