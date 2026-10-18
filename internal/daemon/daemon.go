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
	"github.com/bethropolis/kcd/internal/discovery"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/notification"
	"github.com/bethropolis/kcd/internal/plugins/runcommand"
	"github.com/bethropolis/kcd/internal/plugins/sftp"
	"github.com/bethropolis/kcd/internal/protocol"
)

// Version is set from main's ldflags-provided version at startup.
// Defaults to "dev" for local builds without ldflags.
var Version = "dev"

// shutdownUnmountBudget bounds total shutdown unmount wait: per-mount cost
// can hit 13s, so the budget — not the cost — fits TimeoutStopSec.
const shutdownUnmountBudget = 5 * time.Second

// syncReconnectBroadcast runs UDP broadcast (reconnect owner) while any pair
// is offline, else withdraws it. Pure state, no timers; callers drive it.
func syncReconnectBroadcast(ctx context.Context, devices *device.Registry, bc *discovery.BroadcasterController) {
	for _, dev := range devices.List() {
		if dev.State() == device.StatePaired && !dev.IsConnected() {
			bc.StartOwned(ctx, discovery.OwnerReconnect)
			return
		}
	}
	bc.StopOwned(discovery.OwnerReconnect)
}

func restoredPairingState(state device.PairingState) device.PairingState {
	switch state {
	case device.StateUnknown, device.StatePairRequested, device.StatePairRequestedByPeer:
		return device.StateUnpaired
	default:
		return state
	}
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
	statePath := config.StatePath()
	if loaded, err := device.LoadDevices(statePath); err == nil {
		for _, info := range loaded {
			info.State = restoredPairingState(info.State)
			// Migrate pre-fix names: decode decimal byte escapes to real UTF-8
			// (SanitizeDeviceName already decodes; don't double-decode).
			info.Name = protocol.SanitizeDeviceName(info.Name)
			dev := device.NewDevice(info.ID, info.Name, info.Type, logger)
			dev.SetState(info.State)
			dev.CertFP = info.CertFP
			dev.SetLastSeen(info.LastSeen)
			// Restore the dial target so auto-dial works immediately after a
			// restart (validation lives inside DialTarget).
			if ip, port := info.DialTarget(); ip != nil {
				dev.SetLastIP(ip)
				dev.SetLastPort(port)
			}
			devices.Add(dev)
		}
	} else {
		logger.Warn("failed to load devices", log.Error(err))
	}

	pruneThreshold, _ := time.ParseDuration(cfg.PruneStaleThreshold)
	if pruned := devices.Prune(pruneThreshold); pruned > 0 {
		logger.Info("pruned stale devices on startup", log.Int("count", pruned))
	}

	// Helper to save device state
	saveDevices := func() {
		devices.Prune(pruneThreshold)
		devs := devices.List()
		infos := make([]device.DeviceInfo, 0, len(devs))
		for _, dev := range devs {
			info := device.DeviceInfo{
				ID:       dev.ID(),
				Name:     dev.Name(),
				Type:     dev.Type,
				State:    dev.State(),
				CertFP:   dev.CertFP,
				LastSeen: dev.LastSeen(),
				LastPort: dev.LastPort(),
			}
			// net.IP.String() on nil renders "<nil>" — store empty instead.
			if ip := dev.LastIP(); ip != nil {
				info.LastIP = ip.String()
			}
			infos = append(infos, info)
		}
		_ = device.SaveDevices(statePath, infos)
	}

	// 3. Plugin Registry
	plugins := plugin.NewRegistry(logger)

	var localCert *x509.Certificate
	if len(certPair.Certificate) > 0 {
		localCert, _ = x509.ParseCertificate(certPair.Certificate[0])
	}

	pairPlugin := setupPlugins(cfg, bus, tlsCfg, logger, devices, localCert, saveDevices, plugins)

	// 4. Broadcast Controller (starts stopped — activated by `kcd pair`)
	incomingCaps, outgoingCaps := plugins.Capabilities()
	identity, err := buildIdentity(cfg, incomingCaps, outgoingCaps)
	if err != nil {
		return err
	}

	bc := discovery.NewBroadcasterController(identity, cfg.TCPPort, config.Duration(cfg.Discovery.BroadcastInterval), logger, devices.AllPairedDevicesConnected, config.Duration(cfg.Discovery.BroadcastIdleInterval))

	// mDNS advertisement is always on: responder-only means zero idle timers,
	// so phones keep a standing path while UDP broadcast is stopped.
	go discovery.AdvertiseMDNS(ctx, identity, logger)

	// Advertise over UDP while a pair is offline so it can find us back;
	// fully stopped otherwise. Ownership tracking keeps pairing broadcasts
	// independent.
	go func() {
		sub := bus.Subscribe(0, events.TypeDeviceConnected, events.TypeDeviceDisconnected)
		defer sub.Close()
		sync := func() { syncReconnectBroadcast(ctx, devices, bc) }
		sync()
		for range sub.C {
			sync()
		}
	}()

	// 5. IPC Server
	handler := ipc.NewHandler(devices, plugins, pairPlugin, statePath, bus, pruneThreshold)
	handler.SetPairListenTimeout(config.Duration(cfg.Pairing.ListenTimeout))

	registerIPCRoutes(handler, cfg, devices, plugins, bc, ctx, tlsCfg, logger, startedAt)

	// Explicit `kcd pair <id>` dials on demand: background dial never touches
	// strangers, so arm a one-shot flag and dial now if last-seen is known.
	handler.SetPairDialHook(func(deviceID string) error {
		dev, ok := devices.Get(deviceID)
		if !ok {
			return fmt.Errorf("device not found")
		}
		if dev.State() == device.StatePaired {
			return fmt.Errorf("device already paired")
		}
		dev.RequestPairDial(config.Duration(cfg.Pairing.IntentTTL))
		ip, port := dev.DiscoveryAddr()
		if ip == nil {
			// No address yet — the flagged dial fires on the next
			// broadcast from the phone; tell the user to retry then.
			return fmt.Errorf("device address unknown yet, wait for discovery and retry")
		}
		if port == 0 {
			port = cfg.TCPPort
		}
		go func() {
			DialDevice(ctx, ip, port, deviceID, protocol.ProtocolVersion, identity, tlsCfg, devices, plugins, cfg.DeviceID, logger, true, cfg)
			if !dev.IsConnected() {
				logger.Warn("on-demand pair dial failed", log.String("device_id", deviceID))
				return
			}
			// A successful immediate dial consumes the one-shot trigger; on
			// failure it stays for the next sighting. PairDialActive outlives it.
			dev.ConsumePairDial()
			if dev.State() == device.StatePairRequestedByPeer {
				if err := pairPlugin.AcceptPairing(dev); err != nil {
					logger.Warn("auto-accept on pair dial failed", log.Error(err))
				}
			} else if dev.State() != device.StatePaired {
				// No CLI is attached to this dial, so the verification code
				// only reaches the log; RequestPairing already logs it.
				if _, err := pairPlugin.RequestPairing(dev); err != nil {
					logger.Warn("pair request on dial failed", log.Error(err))
				}
			}
		}()
		return nil
	})

	ipcServer := ipc.NewServer(cfg.SocketPath, handler, logger)

	// Start IPC in background
	go func() {
		if err := ipcServer.Listen(ctx); err != nil {
			logger.Error("ipc server error", log.Error(err))
		}
	}()

	// 6. Transport Layer
	go runTransport(ctx, tlsCfg, bc, identity, devices, plugins, cfg.DeviceID, logger, cfg)

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
					logger.Error("config reload failed", log.Error(err))
					continue
				}

				// Reload RunCommand plugin commands.
				if pl, ok := plugins.GetByName("RunCommand"); ok {
					rc := pl.(*runcommand.RunCommandPlugin)
					rc.Mu.Lock()
					rc.Commands = newCfg.Commands
					rc.CommandsPerDevice = newCfg.CommandsPerDevice
					rc.Mu.Unlock()
					logger.Info("reloaded commands",
						log.Int("global", len(newCfg.Commands)),
						log.Int("per_device", len(newCfg.CommandsPerDevice)),
					)
				}

				// Reload notification filters.
				if pl, ok := plugins.GetByName("Notification"); ok {
					pl.(*notification.NotificationPlugin).SetFilters(newCfg.Notifications.Filters())
					logger.Info("reloaded notification filters")
				}

				// Reload log level.
				logger.SetLevel(newCfg.LogLevel)
			}
		}
	}()

	<-ctx.Done()

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
