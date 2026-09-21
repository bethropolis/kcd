package daemon

import (
	"context"
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
		return pluginRoute(req, &p, plugins, "SFTP", func(pl plugin.Plugin) ipc.Response {
			info := pl.(*sftp.SftpPlugin).Info(p.DeviceID)
			if info == nil {
				return ipc.Response{OK: false, Error: "no SFTP credentials cached for this device — use 'kcd sftp request' first"}
			}
			return jsonOK(info)
		})
	})
	handler.Register(ipc.CmdSftpVolumes, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		return pluginRoute(req, &p, plugins, "SFTP", func(pl plugin.Plugin) ipc.Response {
			volumes := pl.(*sftp.SftpPlugin).Volumes(p.DeviceID)
			if volumes == nil {
				return ipc.Response{OK: false, Error: "no volumes available — use 'kcd sftp request' first"}
			}
			return jsonOK(volumes)
		})
	})
	handler.Register(ipc.CmdSftpMount, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		return deviceRoute(req, &p, devices, plugins, "SFTP", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
			if err := pl.(*sftp.SftpPlugin).RequestMount(dev); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
	})
	handler.Register(ipc.CmdSftpMountLocal, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		return deviceRoute(req, &p, devices, plugins, "SFTP", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
			browsePath, err := pl.(*sftp.SftpPlugin).RequestAndMount(context.Background(), dev)
			if err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return jsonOK(map[string]string{"path": browsePath})
		})
	})
	handler.Register(ipc.CmdSftpUnmount, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		return pluginRoute(req, &p, plugins, "SFTP", func(pl plugin.Plugin) ipc.Response {
			if err := pl.(*sftp.SftpPlugin).Unmount(p.DeviceID); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
	})
	handler.Register(ipc.CmdSftpBrowse, func(req ipc.Request) ipc.Response {
		var p ipc.SftpBrowsePayload
		return deviceRoute(req, &p, devices, plugins, "SFTP", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
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

			return jsonOK(ipc.SftpBrowseResponse{
				Path:    mountPath,
				Volumes: vols,
			})
		})
	})
}
