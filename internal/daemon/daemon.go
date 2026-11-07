package daemon

import (
	"context"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/discovery"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/hooks"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/sftp"
	"github.com/bethropolis/kcd/internal/protocol"
)

// Version is set from main's ldflags-provided version at startup.
// Defaults to "dev" for local builds without ldflags.
var Version = "dev"

// shutdownUnmountBudget bounds total shutdown unmount wait: per-mount cost
// can hit 13s, so the budget — not the cost — fits TimeoutStopSec.
const shutdownUnmountBudget = 5 * time.Second

// syncReconnectBroadcast runs UDP broadcast while any paired device is offline.
func syncReconnectBroadcast(ctx context.Context, devices *device.Registry, bc *discovery.BroadcasterController) {
	syncReconnectBroadcastUntil(ctx, devices, bc, 0)
}

func syncReconnectBroadcastUntil(ctx context.Context, devices *device.Registry, bc *discovery.BroadcasterController, staleAfter time.Duration) time.Time {
	var earliest time.Time
	wanted := false
	for _, dev := range devices.List() {
		if dev.State() == device.StatePaired && !dev.IsConnected() {
			lastSeen := dev.LastSeen()
			if staleAfter > 0 && !lastSeen.IsZero() && time.Since(lastSeen) >= staleAfter {
				continue
			}
			wanted = true
			bc.StartOwned(ctx, discovery.OwnerReconnect)
			if staleAfter > 0 && !lastSeen.IsZero() {
				expires := lastSeen.Add(staleAfter)
				if earliest.IsZero() || expires.Before(earliest) {
					earliest = expires
				}
			}
		}
	}
	if !wanted {
		bc.StopOwned(discovery.OwnerReconnect)
	} else {
		return earliest
	}
	return time.Time{}
}

// Run starts the core daemon lifecycle.
// buildIdentity assembles the daemon's advertised identity from config,
// including the configured device type rather than a hardcoded one.
func buildIdentity(cfg *config.Config, incoming, outgoing []string) (*protocol.Packet, error) {
	return protocol.NewIdentityPacket(cfg.DeviceID, cfg.DeviceName, cfg.DeviceType, cfg.TCPPort, incoming, outgoing)
}

func Run(ctx context.Context, cfg *config.Config) error {
	startedAt := time.Now()

	logger, err := log.New(cfg.LogLevel)
	if err != nil {
		return err
	}
	defer logger.Sync() //nolint:errcheck

	logger.Info("kcd daemon initializing", log.String("device_id", cfg.DeviceID))

	if err := cfg.Validate(); err != nil {
		logger.Fatal("invalid configuration", log.Error(err))
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

	sm, requestSave, saveDevices, err := initStateManager(ctx, cfg, devices, logger)
	if err != nil {
		return err
	}

	// 3. Plugin Registry
	plugins := plugin.NewRegistry(logger)

	var localCert *x509.Certificate
	if len(certPair.Certificate) > 0 {
		localCert, _ = x509.ParseCertificate(certPair.Certificate[0])
	}

	pairPlugin := setupPlugins(cfg, bus, tlsCfg, logger, devices, localCert, requestSave, plugins)
	hooks.New(cfg.Hooks, bus, logger).Start(ctx)

	// 4. Broadcast Controller (starts stopped — activated by `kcd pair`)
	incomingCaps, outgoingCaps := plugins.Capabilities()

	tcpListener, err := bindTCPListener(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer tcpListener.Close()
	tcpListener.SetKeepAliveIdle(config.Duration(cfg.Network.KeepAliveIdle))

	identity, err := buildIdentity(cfg, incomingCaps, outgoingCaps)
	if err != nil {
		return err
	}

	env := &transportEnv{
		tls:      tlsCfg,
		identity: identity,
		devices:  devices,
		plugins:  plugins,
		localID:  cfg.DeviceID,
		cfg:      cfg,
		logger:   logger,
	}

	bc := discovery.NewBroadcasterController(identity, protocol.DefaultDiscoveryPort, config.Duration(cfg.Discovery.BroadcastInterval), logger, devices.AllPairedDevicesConnected, config.Duration(cfg.Discovery.BroadcastIdleInterval))
	go discovery.WatchNetworkChanges(ctx, bc.ResetBackoff)

	// mDNS advertisement is always on: responder-only means zero idle timers,
	// so phones keep a standing path while UDP broadcast is stopped.
	go discovery.AdvertiseMDNS(ctx, identity, logger)

	// Advertise over UDP while a pair is offline so it can find us back;
	// fully stopped otherwise. Ownership tracking keeps pairing broadcasts
	// independent.
	go func() {
		sub := bus.Subscribe(0, events.TypeDeviceConnected, events.TypeDeviceDisconnected)
		defer sub.Close()
		var timer *time.Timer
		var timerC <-chan time.Time
		reset := func() {
			expires := syncReconnectBroadcastUntil(ctx, devices, bc, config.Duration(cfg.Reconnect.StaleAfter))
			if expires.IsZero() {
				if timer != nil {
					timer.Stop()
				}
				timerC = nil
				return
			}
			if timer == nil {
				timer = time.NewTimer(time.Until(expires))
			} else if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			delay := time.Until(expires)
			if delay < 0 {
				delay = 0
			}
			timer.Reset(delay)
			timerC = timer.C
		}
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()
		reset()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-sub.C:
				if !ok {
					return
				}
				reset()
			case <-timerC:
				reset()
			}
		}
	}()

	// 5. IPC Server
	pairDialHook := makePairDialHook(ctx, env, pairPlugin)
	handler := ipc.NewHandler(devices, plugins, bus)
	registerPairRoutes(handler, cfg, devices, plugins, bus, saveDevices, sm.pruneThreshold, pairDialHook)
	registerIPCRoutes(handler, env, bc, ctx, startedAt)

	ipcServer := ipc.NewServer(cfg.SocketPath, handler, logger)

	// Start IPC in background
	go func() {
		if err := ipcServer.Listen(ctx); err != nil {
			logger.Error("ipc server error", log.Error(err))
		}
	}()
	if cfg.IPC.ReadOnlySocket != "" && cfg.IPC.ReadOnlySocket != cfg.SocketPath {
		readOnlyServer := ipc.NewReadOnlyServer(cfg.IPC.ReadOnlySocket, handler, logger)
		go func() {
			if err := readOnlyServer.Listen(ctx); err != nil {
				logger.Error("read-only ipc server error", log.Error(err))
			}
		}()
	}

	// 6. Transport Layer
	go runTransport(ctx, env, bc, tcpListener)

	notifySystemdReady()

	watchConfigReload(ctx, cfg, devices, plugins, logger)

	<-ctx.Done()
	sm.wait()

	logger.Info("kcd daemon shutting down")

	// Release SFTP mounts before returning: graceful stops skip OnDisconnect,
	// so mounts would stay live. Bounded well under TimeoutStopSec.
	if pl, ok := plugins.GetByName("SFTP"); ok {
		unmountCtx, unmountCancel := context.WithTimeout(context.Background(), shutdownUnmountBudget)
		pl.(*sftp.SftpPlugin).UnmountAll(unmountCtx)
		unmountCancel()
	}

	acquires, misses := protocol.PoolStats()
	hits := acquires - misses
	logger.Debug("packet pool stats",
		log.Int64("acquires", acquires),
		log.Int64("misses", misses),
		log.Int64("hits", hits),
		log.String("hit_rate", fmt.Sprintf("%.1f%%",
			float64(hits)/max(float64(acquires), 1)*100,
		)),
	)

	return nil
}
