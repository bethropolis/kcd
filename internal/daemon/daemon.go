package daemon

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/battery"
	"github.com/bethropolis/kcd/internal/plugins/clipboard"
	"github.com/bethropolis/kcd/internal/plugins/connectivity"
	"github.com/bethropolis/kcd/internal/plugins/findmyphone"
	"github.com/bethropolis/kcd/internal/plugins/lockdevice"
	"github.com/bethropolis/kcd/internal/plugins/mousepad"
	"github.com/bethropolis/kcd/internal/plugins/mpris"
	"github.com/bethropolis/kcd/internal/plugins/notification"
	"github.com/bethropolis/kcd/internal/plugins/pair"
	"github.com/bethropolis/kcd/internal/plugins/ping"
	"github.com/bethropolis/kcd/internal/plugins/runcommand"
	"github.com/bethropolis/kcd/internal/plugins/sendnotification"
	"github.com/bethropolis/kcd/internal/plugins/sftp"
	"github.com/bethropolis/kcd/internal/plugins/share"
	"github.com/bethropolis/kcd/internal/plugins/sms"
	"github.com/bethropolis/kcd/internal/plugins/systemvolume"
	"github.com/bethropolis/kcd/internal/plugins/telephony"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// version is set via ldflags at build time.
var version = "dev"

// Run starts the core daemon lifecycle.
func Run(ctx context.Context, cfg *config.Config) error {
	startedAt := time.Now()

	atomicLevel := zap.NewAtomicLevel()
	switch cfg.LogLevel {
	case "debug":
		atomicLevel.SetLevel(zapcore.DebugLevel)
	case "warn":
		atomicLevel.SetLevel(zapcore.WarnLevel)
	case "error", "quiet":
		atomicLevel.SetLevel(zapcore.ErrorLevel)
	default:
		atomicLevel.SetLevel(zapcore.InfoLevel)
	}

	var zapCfg zap.Config
	if cfg.LogLevel == "debug" {
		zapCfg = zap.NewDevelopmentConfig()
	} else {
		zapCfg = zap.NewProductionConfig()
	}
	zapCfg.Level = atomicLevel

	logger, err := zapCfg.Build()
	if err != nil {
		return err
	}
	defer logger.Sync() //nolint:errcheck

	logger.Info("kcd daemon initializing", zap.String("device_id", cfg.DeviceID))

	if err := cfg.Validate(); err != nil {
		logger.Fatal("invalid configuration", zap.Error(err))
	}

	// 1. TLS Certs (CN must match device ID for KDE Connect verification)
	certPair, err := cert.LoadOrGenerate(cfg.CertFile, cfg.KeyFile, cfg.DeviceID)
	if err != nil {
		return err
	}
	tlsCfg := cert.TLSConfig(certPair)

	// 2. Event Bus & Device Registry
	bus := events.NewBus(logger)
	devices := device.NewRegistry(bus)
	statePath := config.StatePath()
	if loaded, err := device.LoadDevices(statePath); err == nil {
		for _, info := range loaded {
			dev := device.NewDevice(info.ID, info.Name, info.Type, logger)
			dev.SetState(info.State)
			dev.CertFP = info.CertFP
			dev.SetLastSeen(info.LastSeen)
			devices.Add(dev)
		}
	} else {
		logger.Warn("failed to load devices", zap.Error(err))
	}

	// Helper to save device state
	saveDevices := func() {
		devs := devices.List()
		infos := make([]device.DeviceInfo, 0, len(devs))
		for _, dev := range devs {
			infos = append(infos, device.DeviceInfo{
				ID:       dev.ID(),
				Name:     dev.Name(),
				Type:     dev.Type,
				State:    dev.State(),
				CertFP:   dev.CertFP,
				LastSeen: dev.LastSeen(),
			})
		}
		_ = device.SaveDevices(statePath, infos)
	}

	// 3. Plugin Registry
	plugins := plugin.NewRegistry(logger)

	// Parse local cert for verification keys
	var localCert *x509.Certificate
	if len(certPair.Certificate) > 0 {
		localCert, _ = x509.ParseCertificate(certPair.Certificate[0])
	}

	pairPlugin := pair.NewPairPlugin(devices, localCert, cfg.AutoAcceptPairing, cfg.Pairing, saveDevices, bus, logger)
	plugins.Register(pairPlugin)
	if cfg.Plugins.Battery {
		plugins.Register(battery.NewBatteryPlugin(cfg.Battery, bus, logger))
	}
	if cfg.Plugins.Notification {
		plugins.Register(notification.NewNotificationPlugin(cfg.Notification, bus, tlsCfg, logger))
	}
	if cfg.Plugins.Clipboard {
		plugins.Register(clipboard.NewClipboardPlugin(tlsCfg, logger))
	}
	if cfg.Plugins.Share {
		plugins.Register(share.NewSharePlugin(cfg.DownloadDir, cfg.Share, tlsCfg, bus, logger))
	}
	if cfg.Plugins.RunCommand {
		plugins.Register(runcommand.NewRunCommandPlugin(cfg.Commands, logger))
	}
	if cfg.Plugins.Ping {
		plugins.Register(ping.NewPingPlugin(cfg.Ping, bus, logger))
	}
	if cfg.Plugins.Telephony {
		plugins.Register(telephony.NewTelephonyPluginWithOptions(bus, cfg.Plugins.PauseMusic, logger))
	}
	if cfg.Plugins.Connectivity {
		plugins.Register(connectivity.NewConnectivityPlugin(bus))
	}
	if cfg.Plugins.MPRIS {
		plugins.Register(mpris.NewMPRISPlugin(tlsCfg, logger))
	}
	if cfg.Plugins.Mousepad {
		plugins.Register(mousepad.NewMousepadPlugin(cfg.Mousepad, logger))
	}
	if cfg.Plugins.SFTP {
		plugins.Register(sftp.NewSftpPlugin(cfg.SFTP, bus, logger))
	}
	if cfg.Plugins.FindMyPhone {
		plugins.Register(&findmyphone.FindMyPhonePlugin{})
	}
	if cfg.Plugins.LockDevice {
		plugins.Register(lockdevice.NewLockDevicePlugin(logger))
	}
	if cfg.Plugins.SystemVolume {
		plugins.Register(systemvolume.NewSystemVolumePlugin(bus, logger))
	}
	if cfg.Plugins.SendNotifications {
		plugins.Register(sendnotification.NewSendNotificationPlugin(logger, devices))
	}
	if cfg.Plugins.SMS {
		plugins.Register(sms.NewSMSPlugin())
	}

	// 4. IPC Server
	handler := ipc.NewHandler(devices, plugins, pairPlugin, statePath, bus)

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

	ipcServer := ipc.NewServer(cfg.SocketPath, handler, logger)

	// Start IPC in background
	go func() {
		if err := ipcServer.Listen(ctx); err != nil {
			logger.Error("ipc server error", zap.Error(err))
		}
	}()

	// 5. Transport Layer
	incomingCaps, outgoingCaps := plugins.Capabilities()
	identity, err := protocol.NewIdentityPacket(cfg.DeviceID, cfg.DeviceName, "desktop", 1716, incomingCaps, outgoingCaps)
	if err != nil {
		return err
	}

	go runTransport(ctx, tlsCfg, cfg.EnableBroadcast, identity, devices, plugins, cfg.DeviceID, logger)

	// Wait for context cancellation (SIGTERM)
	if notifySocket := os.Getenv("NOTIFY_SOCKET"); notifySocket != "" {
		addr := &net.UnixAddr{Name: notifySocket, Net: "unixgram"}
		if strings.HasPrefix(notifySocket, "@") {
			addr.Name = "\x00" + notifySocket[1:]
		}
		if conn, err := net.DialUnix("unixgram", nil, addr); err == nil {
			_, _ = conn.Write([]byte("READY=1\n"))
			conn.Close()
		}
	}

	// Hot-reload on SIGHUP.
	sighupCh := make(chan os.Signal, 1)
	signal.Notify(sighupCh, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sighupCh:
				logger.Info("SIGHUP received, reloading config")
				newCfg, err := config.Load(cfg.ConfigPath)
				if err != nil {
					logger.Error("config reload failed", zap.Error(err))
					continue
				}

				// Reload RunCommand plugin commands.
				if pl, ok := plugins.GetByName("RunCommand"); ok {
					rc := pl.(*runcommand.RunCommandPlugin)
					rc.Mu.Lock()
					rc.Commands = newCfg.Commands
					rc.Mu.Unlock()
					logger.Info("reloaded commands", zap.Int("count", len(newCfg.Commands)))
				}

				// Reload notification filters.
				if pl, ok := plugins.GetByName("Notification"); ok {
					pl.(*notification.NotificationPlugin).SetFilters(newCfg.Notifications)
					logger.Info("reloaded notification filters")
				}

				// Reload log level.
				setLogLevel(atomicLevel, newCfg.LogLevel)
			}
		}
	}()

	<-ctx.Done()

	logger.Info("kcd daemon shutting down")

	acquires, misses := protocol.PoolStats()
	hits := acquires - misses
	logger.Debug("packet pool stats",
		zap.Int64("acquires", acquires),
		zap.Int64("misses", misses),
		zap.Int64("hits", hits),
		zap.String("hit_rate", fmt.Sprintf("%.1f%%",
			float64(hits)/max(float64(acquires), 1)*100,
		)),
	)

	return nil
}

// setLogLevel updates the atomic log level without restarting the daemon.
func setLogLevel(al zap.AtomicLevel, level string) {
	switch level {
	case "debug":
		al.SetLevel(zapcore.DebugLevel)
	case "warn":
		al.SetLevel(zapcore.WarnLevel)
	case "error", "quiet":
		al.SetLevel(zapcore.ErrorLevel)
	default:
		al.SetLevel(zapcore.InfoLevel)
	}
}
