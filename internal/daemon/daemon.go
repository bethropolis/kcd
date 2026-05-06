package daemon

import (
	"context"
	"crypto/x509"
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
	"github.com/bethropolis/kcd/internal/plugins/notification"
	"github.com/bethropolis/kcd/internal/plugins/runcommand"
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

	pairPlugin := setupPlugins(cfg, bus, tlsCfg, logger, devices, localCert, saveDevices, plugins)

	// 4. IPC Server
	handler := ipc.NewHandler(devices, plugins, pairPlugin, statePath, bus)

	registerIPCRoutes(handler, cfg, devices, plugins, ctx, tlsCfg, logger, startedAt)

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
