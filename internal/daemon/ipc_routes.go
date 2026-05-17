package daemon

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/mpris"
	"github.com/bethropolis/kcd/internal/plugins/notification"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

func registerIPCRoutes(handler *ipc.Handler, cfg *config.Config, devices *device.Registry, plugins *plugin.Registry, ctx context.Context, tlsCfg *tls.Config, logger *zap.Logger, startedAt time.Time) {
	if cfg.Plugins.Notification {
		if notifPl, ok := plugins.GetByName("Notification"); ok {
			notifPl.(*notification.NotificationPlugin).SetFilters(cfg.Notifications)
		}
	}

	if cfg.Plugins.Battery {
		registerBatteryRoutes(handler, devices)
	}
	if cfg.Plugins.Clipboard {
		registerClipboardRoutes(handler, devices, plugins)
	}
	if cfg.Plugins.RunCommand {
		registerRunCommandRoutes(handler, devices)
	}
	if cfg.Plugins.Share {
		registerShareRoutes(handler, devices, plugins)
	}
	if cfg.Plugins.SFTP {
		registerSftpRoutes(handler, devices, plugins)
	}
	registerCommRoutes(handler, cfg, devices, plugins)
	registerDeviceRoutes(handler, cfg, devices, plugins)

	handler.Register(ipc.CmdConnect, func(req ipc.Request) ipc.Response {
		var p ipc.ConnectPayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		addr := net.ParseIP(p.IP)
		if addr == nil {
			return ipc.Response{OK: false, Error: "invalid IP address"}
		}

		go func() {
			incomingCaps, outgoingCaps := plugins.Capabilities()
			identityPkt, err := protocol.NewIdentityPacket(cfg.DeviceID, cfg.DeviceName, "desktop", 1716, incomingCaps, outgoingCaps)
			if err != nil {
				return
			}
			DialDevice(ctx, addr, 1716, "manual", protocol.ProtocolVersion, identityPkt, tlsCfg, devices, plugins, cfg.DeviceID, logger)
		}()

		return ipc.Response{OK: true}
	})

	handler.Register(ipc.CmdStatus, func(req ipc.Request) ipc.Response {
		uptime := time.Since(startedAt)
		h := int(uptime.Hours())
		m := int(uptime.Minutes()) % 60
		uptimeHuman := fmt.Sprintf("%dh %dm", h, m)

		pluginNames := make([]string, 0)
		for _, p := range plugins.All() {
			pluginNames = append(pluginNames, p.Name())
		}

		total := 0
		connected := 0
		for _, d := range devices.List() {
			total++
			if d.IsConnected() {
				connected++
			}
		}

		data, _ := json.Marshal(ipc.StatusResponse{
			Version:        version,
			StartedAt:      startedAt.UTC().Format(time.RFC3339),
			UptimeHuman:    uptimeHuman,
			SocketPath:     cfg.SocketPath,
			ConfigPath:     cfg.ConfigPath,
			Plugins:        pluginNames,
			DeviceCount:    total,
			ConnectedCount: connected,
		})
		return ipc.Response{OK: true, Data: data}
	})

	handler.Register(ipc.CmdMprisStatus, func(req ipc.Request) ipc.Response {
		pl, ok := plugins.GetByName("MPRIS")
		if !ok {
			return ipc.Response{OK: false, Error: "mpris plugin not enabled"}
		}
		status := pl.(*mpris.MPRISPlugin).DebugStatus()
		data, _ := json.Marshal(status)
		return ipc.Response{OK: true, Data: data}
	})
}
