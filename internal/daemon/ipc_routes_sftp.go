package daemon

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/sftp"
)

// resolveVolume resolves a user-supplied volume argument (index, name, or path)
// against a list of StorageVolume. Returns the matching path or empty string.
func resolveVolume(arg string, volumes []ipc.StorageVolumeResponse) string {
	if len(volumes) == 0 {
		return ""
	}

	// Try index first.
	if idx, err := strconv.Atoi(arg); err == nil && idx >= 0 && idx < len(volumes) {
		return volumes[idx].Path
	}

	// Try name match (case-insensitive).
	for _, v := range volumes {
		if strings.EqualFold(v.Name, arg) {
			return v.Path
		}
	}

	// Try path match (exact).
	for _, v := range volumes {
		if v.Path == arg {
			return v.Path
		}
	}

	// Try path match (case-insensitive).
	for _, v := range volumes {
		if strings.EqualFold(v.Path, arg) {
			return v.Path
		}
	}

	return ""
}

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
	handler.Register(ipc.CmdSftpBrowse, func(req ipc.Request) ipc.Response {
		var p ipc.SftpBrowsePayload
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
		sftpPl := pl.(*sftp.SftpPlugin)

		volumePath := p.Volume

		// If a volume was specified, try to resolve it to a path.
		if volumePath != "" {
			vols := sftpPl.Volumes(p.DeviceID)
			if len(vols) > 0 {
				sv := make([]ipc.StorageVolumeResponse, len(vols))
				for i, v := range vols {
					sv[i] = ipc.StorageVolumeResponse{Name: v.Name, Path: v.Path}
				}
				if resolved := resolveVolume(volumePath, sv); resolved != "" {
					volumePath = resolved
				}
			}
		}

		mountPath, volumes, err := sftpPl.RequestAndMountVolume(context.Background(), dev, volumePath)
		if err != nil {
			return ipc.Response{OK: false, Error: err.Error()}
		}

		vols := make([]ipc.StorageVolumeResponse, len(volumes))
		for i, v := range volumes {
			vols[i] = ipc.StorageVolumeResponse{Name: v.Name, Path: v.Path}
		}

		resp := ipc.SftpBrowseResponse{
			Path:    mountPath,
			Volumes: vols,
		}
		data, _ := json.Marshal(resp)
		return ipc.Response{OK: true, Data: data}
	})
}
