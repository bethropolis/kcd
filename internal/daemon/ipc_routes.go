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
	"github.com/bethropolis/kcd/internal/discovery"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/mpris"
	"github.com/bethropolis/kcd/internal/plugins/notification"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

func registerIPCRoutes(handler *ipc.Handler, cfg *config.Config, devices *device.Registry, plugins *plugin.Registry, bc *discovery.BroadcasterController, ctx context.Context, tlsCfg *tls.Config, logger *zap.Logger, startedAt time.Time) {
	if cfg.Plugins.Notification {
		if notifPl, ok := plugins.GetByName("Notification"); ok {
			notifPl.(*notification.NotificationPlugin).SetFilters(cfg.Notifications.Filters())
		}
	}

	if cfg.Plugins.Battery {
		registerBatteryRoutes(handler, devices)
	}
	if cfg.Plugins.Connectivity {
		registerConnectivityRoutes(handler, devices, plugins)
	}
	if cfg.Plugins.Clipboard {
		registerClipboardRoutes(handler, devices, plugins)
	}
	if cfg.Plugins.Contacts {
		registerContactsRoutes(handler, devices, plugins)
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
	if cfg.Plugins.MPRIS {
		registerMprisRoutes(handler, devices, plugins)
	}
	if cfg.Plugins.RemoteSystemVolume {
		registerRemoteVolumeRoutes(handler, devices, plugins)
	}

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
			identityPkt, err := protocol.NewIdentityPacket(cfg.DeviceID, cfg.DeviceName, "desktop", cfg.TCPPort, incomingCaps, outgoingCaps)
			if err != nil {
				return
			}
			// No target ID: the peer is whoever answers at this address.
			// An empty target omits targetDeviceId from the pre-TLS
			// identity; stock peers drop dials addressed to anyone else.
			DialDevice(ctx, addr, cfg.TCPPort, "", protocol.ProtocolVersion, identityPkt, tlsCfg, devices, plugins, cfg.DeviceID, logger, true, cfg)
		}()

		return ipc.Response{OK: true}
	})

	handler.Register(ipc.CmdBroadcastStart, func(req ipc.Request) ipc.Response {
		bc.Start(ctx)
		return ipc.Response{OK: true}
	})
	handler.Register(ipc.CmdBroadcastStop, func(req ipc.Request) ipc.Response {
		bc.Stop()
		// Pairing window closed: drop discovery connections that never led
		// to pairing so strangers don't linger. Paired devices, pair
		// requests in flight, and explicit pair intents (kept alive until
		// pairing starts, ends, or expires) are left alone.
		for _, dev := range devices.List() {
			if !dev.IsConnected() || dev.State() != device.StateUnpaired || dev.PairDialActive() {
				continue
			}
			logger.Debug("broadcast stopped, dropping unpaired discovery connection",
				zap.String("device_id", dev.ID()))
			dev.Disconnect()
		}
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
		devInfos := make([]ipc.StatusDevice, 0)
		for _, d := range devices.List() {
			total++
			isConnected := d.IsConnected()
			if isConnected {
				connected++
			}
			info := ipc.StatusDevice{
				ID:        d.ID(),
				Name:      d.Name(),
				Type:      d.Type,
				State:     d.State().String(),
				Connected: isConnected,
			}
			if ip := d.RemoteIP(); ip != nil && isConnected {
				info.Addr = ip.String()
				if port := d.LastPort(); port > 0 {
					info.Addr += fmt.Sprintf(":%d", port)
				}
			} else if ip := d.LastIP(); ip != nil {
				info.Addr = ip.String()
			}
			if charge, charging := d.GetBattery(); d.HasBattery() {
				info.Battery = &ipc.StatusBattery{
					Charge:   charge,
					Charging: charging,
					AgeMs:    d.BatteryAge().Milliseconds(),
				}
			}
			if lastSeen := d.LastSeen(); !lastSeen.IsZero() {
				info.LastSeen = lastSeen.UTC().Format(time.RFC3339)
			}
			devInfos = append(devInfos, info)
		}

		data, _ := json.Marshal(ipc.StatusResponse{
			Version:        Version,
			StartedAt:      startedAt.UTC().Format(time.RFC3339),
			UptimeHuman:    uptimeHuman,
			SocketPath:     cfg.SocketPath,
			ConfigPath:     cfg.ConfigPath,
			TCPPort:        cfg.TCPPort,
			Plugins:        pluginNames,
			DeviceCount:    total,
			ConnectedCount: connected,
			Devices:        devInfos,
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
