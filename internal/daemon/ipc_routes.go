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
	"github.com/bethropolis/kcd/internal/plugins/clipboard"
	"github.com/bethropolis/kcd/internal/plugins/findmyphone"
	"github.com/bethropolis/kcd/internal/plugins/lockdevice"
	"github.com/bethropolis/kcd/internal/plugins/mpris"
	"github.com/bethropolis/kcd/internal/plugins/notification"
	"github.com/bethropolis/kcd/internal/plugins/sftp"
	"github.com/bethropolis/kcd/internal/plugins/share"
	"github.com/bethropolis/kcd/internal/plugins/sms"
	"github.com/bethropolis/kcd/internal/plugins/telephony"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

func registerIPCRoutes(handler *ipc.Handler, cfg *config.Config, devices *device.Registry, plugins *plugin.Registry, ctx context.Context, tlsCfg *tls.Config, logger *zap.Logger, startedAt time.Time) {
	// Apply notification filters from config.
	if cfg.Plugins.Notification {
		if notifPl, ok := plugins.GetByName("Notification"); ok {
			notifPl.(*notification.NotificationPlugin).SetFilters(cfg.Notifications)
		}
	}

	// Register Plugin IPC handlers
	if cfg.Plugins.Battery {
		handler.Register(ipc.CmdBattery, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			charge, charging := dev.GetBattery()
			data, _ := json.Marshal(map[string]interface{}{
				"charge":   charge,
				"charging": charging,
			})
			return ipc.Response{OK: true, Data: data}
		})
	}

	if cfg.Plugins.Clipboard {
		handler.Register(ipc.CmdClipboardPush, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			pl, ok := plugins.GetByName("Clipboard")
			if !ok {
				return ipc.Response{OK: false, Error: "clipboard plugin not enabled"}
			}
			if err := clipboard.Push(context.Background(), dev, pl.(*clipboard.ClipboardPlugin)); err != nil {
				return ipc.Response{OK: false, Error: "clipboard push failed: " + err.Error()}
			}
			return ipc.Response{OK: true}
		})
	}

	if cfg.Plugins.RunCommand {
		handler.Register(ipc.CmdRunList, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			pkt, _ := protocol.NewPacket("kdeconnect.runcommand.request", map[string]bool{"requestCommandList": true})
			if err := dev.Send(pkt); err != nil {
				return ipc.Response{OK: false, Error: "failed to send runcommand list request"}
			}
			return ipc.Response{OK: true}
		})
		handler.Register(ipc.CmdRunExec, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			pkt, _ := protocol.NewPacket("kdeconnect.runcommand.request", map[string]string{"key": p.Key})
			if err := dev.Send(pkt); err != nil {
				return ipc.Response{OK: false, Error: "failed to send runcommand exec request"}
			}
			return ipc.Response{OK: true}
		})
	}

	if cfg.Plugins.Share {
		handler.Register(ipc.CmdShare, func(req ipc.Request) ipc.Response {
			var p ipc.SharePayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			pl, ok := plugins.GetByName("Share")
			if !ok {
				return ipc.Response{OK: false, Error: "share plugin not enabled"}
			}
			sharePl, ok := pl.(*share.SharePlugin)
			if !ok {
				return ipc.Response{OK: false, Error: "invalid share plugin type"}
			}
			if err := sharePl.SendFile(context.Background(), dev, p.FilePath); err != nil {
				return ipc.Response{OK: false, Error: "share failed: " + err.Error()}
			}
			return ipc.Response{OK: true}
		})
	}

	if cfg.Plugins.SFTP {
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
			// RequestAndMount: sends the SFTP request to the phone, waits for
			// credentials, mounts via sshfs, and opens xdg-open automatically.
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

	if cfg.Plugins.SMS {
		handler.Register(ipc.CmdSendSMS, func(req ipc.Request) ipc.Response {
			var p ipc.SMSPayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			pl, ok := plugins.GetByName("SMS")
			if !ok {
				return ipc.Response{OK: false, Error: "sms plugin not enabled"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			if err := pl.(*sms.SMSPlugin).SendSMS(dev, p.PhoneNumber, p.Message); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
	}

	if cfg.Plugins.Telephony {
		handler.Register(ipc.CmdCallMute, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			pl, ok := plugins.GetByName("Telephony")
			if !ok {
				return ipc.Response{OK: false, Error: "telephony plugin not enabled"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			if err := pl.(*telephony.TelephonyPlugin).Mute(dev); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
	}

	if cfg.Plugins.FindMyPhone {
		handler.Register(ipc.CmdFindMyPhone, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			pl, ok := plugins.GetByName("FindMyPhone")
			if !ok {
				return ipc.Response{OK: false, Error: "findmyphone plugin not enabled"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			if err := pl.(*findmyphone.FindMyPhonePlugin).Ring(dev); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
	}

	if cfg.Plugins.LockDevice {
		handler.Register(ipc.CmdLock, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			pl, ok := plugins.GetByName("LockDevice")
			if !ok {
				return ipc.Response{OK: false, Error: "lockdevice plugin not enabled"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			if err := pl.(*lockdevice.LockDevicePlugin).Lock(dev); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
		handler.Register(ipc.CmdUnlock, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			pl, ok := plugins.GetByName("LockDevice")
			if !ok {
				return ipc.Response{OK: false, Error: "lockdevice plugin not enabled"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			if err := pl.(*lockdevice.LockDevicePlugin).Unlock(dev); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
	}

	if cfg.Plugins.Notification {
		handler.Register(ipc.CmdNotifyReply, func(req ipc.Request) ipc.Response {
			var p ipc.NotifyReplyPayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			pl, ok := plugins.GetByName("Notification")
			if !ok {
				return ipc.Response{OK: false, Error: "notification plugin not enabled"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			if err := pl.(*notification.NotificationPlugin).RequestReply(dev, p.ReplyID, p.Message); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
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
			identityPkt, err := protocol.NewIdentityPacket(cfg.DeviceID, cfg.DeviceName, "desktop", 1716, incomingCaps, outgoingCaps)
			if err != nil {
				return
			}
			DialDevice(ctx, addr, 1716, "manual", protocol.ProtocolVersion, identityPkt, tlsCfg, devices, plugins, cfg.DeviceID, logger)
		}()

		return ipc.Response{OK: true}
	})

	// CmdStatus — runtime info
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

	// CmdMprisStatus — MPRIS debug info
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
