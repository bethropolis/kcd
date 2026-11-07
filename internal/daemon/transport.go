package daemon

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/discovery"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/netutil"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
)

// shouldEphemeralClose reports whether a discovery-only connection should
// close on a fresh sighting: pairing mode, paired state, in-flight pair
// traffic, and explicit pair intents all keep it.
func shouldEphemeralClose(dev *device.Device, pairingMode bool) bool {
	if pairingMode {
		return false
	}
	if !dev.EphemeralDialed() {
		return false
	}
	if dev.State() != device.StateUnpaired {
		return false
	}
	// An explicit `kcd pair <id>` intent outlives the one-shot dial trigger
	// (consumed on first sighting) until pairing starts, ends, or expires.
	if dev.PairDialActive() {
		return false
	}
	return true
}

// discoveryDialMinInterval rate-limits ephemeral dials per unknown device:
// without it a chatty announcer spawns a dial storm.
const discoveryDialMinInterval = 2 * time.Second

func bindTCPListener(ctx context.Context, opts *config.Config, logger log.Logger) (*transport.Listener, error) {
	canFallback := opts.TCPPort == 0 || opts.TCPPort == protocol.DefaultTCPPort
	if canFallback {
		for port := protocol.TCPPortMin; port <= protocol.TCPPortMax; port++ {
			l, err := transport.Listen(ctx, fmt.Sprintf(":%d", port))
			if err == nil {
				if port != protocol.DefaultTCPPort {
					logger.Info("default TCP port busy, bound fallback port", log.Int("port", port))
				}
				opts.TCPPort = port
				return l, nil
			}
		}
		return nil, fmt.Errorf("transport: failed to bind TCP listener in range %d-%d", protocol.TCPPortMin, protocol.TCPPortMax)
	}

	l, err := transport.Listen(ctx, fmt.Sprintf(":%d", opts.TCPPort))
	if err != nil {
		return nil, fmt.Errorf("transport: failed to bind TCP listener on port %d: %w", opts.TCPPort, err)
	}
	return l, nil
}

func updateIdentityTCPPort(pkt *protocol.Packet, port int) error {
	if pkt == nil || len(pkt.Body) == 0 {
		return nil
	}
	var body protocol.IdentityBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}
	body.TCPPort = port
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	pkt.Body = data
	return nil
}

func runTransport(ctx context.Context, cfg *tls.Config, bc *discovery.BroadcasterController, identity *protocol.Packet, devices *device.Registry, plugins *plugin.Registry, localDeviceID string, logger log.Logger, opts *config.Config, listener ...*transport.Listener) {
	var tcpListener *transport.Listener
	if len(listener) > 0 && listener[0] != nil {
		tcpListener = listener[0]
	} else {
		var err error
		tcpListener, err = bindTCPListener(ctx, opts, logger)
		if err != nil {
			logger.Error("failed to start TCP listener", log.Error(err))
			return
		}
		defer tcpListener.Close()
		tcpListener.SetKeepAliveIdle(config.Duration(opts.Network.KeepAliveIdle))
		if err := updateIdentityTCPPort(identity, opts.TCPPort); err != nil {
			logger.Warn("failed to update identity with bound TCP port", log.Error(err))
		}
	}

	// Discovery runs timer-free. Stranger dials are throttled (1/s global,
	// 1/5s per IP) against ID-minting peers; paired and intent dials bypass.
	// Strangers get one ephemeral dial per unpaired era to list each other.
	var dialMu sync.Mutex
	lastEphemeralGlobal := time.Now().Add(-time.Minute)
	lastEphemeralByIP := map[string]time.Time{}
	allowEphemeralDial := func(ip net.IP) bool {
		dialMu.Lock()
		defer dialMu.Unlock()
		now := time.Now()
		if now.Sub(lastEphemeralGlobal) < time.Second {
			return false
		}
		if last, ok := lastEphemeralByIP[ip.String()]; ok && now.Sub(last) < 5*time.Second {
			return false
		}
		// Opportunistic prune so spoofed source IPs can't grow the map.
		for k, v := range lastEphemeralByIP {
			if now.Sub(v) > time.Minute {
				delete(lastEphemeralByIP, k)
			}
		}
		lastEphemeralGlobal = now
		lastEphemeralByIP[ip.String()] = now
		return true
	}

	onDeviceFound := func(ip net.IP, tcpPort int, peerIdentity *protocol.Packet) {
		var body protocol.IdentityBody
		if err := json.Unmarshal(peerIdentity.Body, &body); err != nil {
			return
		}

		if body.DeviceID == localDeviceID {
			return
		}
		if !protocol.ValidDeviceID(body.DeviceID) {
			logger.Debug("ignoring discovery with invalid device ID", log.String("device_id", body.DeviceID))
			return
		}

		// Never dial garbage ports from unauthenticated announcements.
		if !validDialPort(tcpPort) {
			logger.Debug("ignoring discovery with invalid tcpPort",
				log.String("device_id", body.DeviceID),
				log.Int("port", tcpPort))
			return
		}

		pairingMode := bc != nil && bc.IsRunning()
		dev, known := devices.Get(body.DeviceID)

		if known && dev.IsConnected() {
			// A fresh sighting of a connected device closes it again if the
			// connection only ever existed for the discovery handshake.
			if shouldEphemeralClose(dev, pairingMode) {
				logger.Debug("closing ephemeral discovery connection",
					log.String("device_id", body.DeviceID))
				dev.Disconnect()
				return
			}
			if dev.State() != device.StatePaired {
				return
			}
		}

		if known && dev.State() == device.StatePaired {
			// A paired sighting proves the peer is alive here: redial when
			// disconnected or roamed, ignore same-IP (read-idle cannot tell
			// zombies from healthy sessions). Setup re-verifies the cert.
			dev.SetLastSeen(time.Now())
			syncReconnectBroadcastUntil(ctx, devices, bc, config.Duration(opts.Reconnect.StaleAfter))
			bc.ResetBackoff()
			if dev.NoteSighting(ip) {
				dev.ResetReconnectAttempt()
			}
			// Instant dial only somewhere new: backoff covers last-known and the
			// phone redials inbound itself, so a second outbound breeds RSTs.
			if lastIP := dev.LastIP(); lastIP == nil || !lastIP.Equal(ip) {
				if dev.ShouldDiscoveryDial(discoveryDialMinInterval) {
					// Prefer the peer's last authenticated listening port
					// over the unauthenticated sighted one.
					port := tcpPort
					if p := dev.LastPort(); validDialPort(p) {
						port = p
					}
					go DialDevice(ctx, ip, port, body.DeviceID, body.ProtocolVersion, identity, cfg, devices, plugins, localDeviceID, logger, false, opts)
				}
			} else if dev.IsConnected() {
				if currIP := dev.RemoteIP(); currIP == nil || !currIP.Equal(ip) {
					// Live socket is bound elsewhere: the peer roamed but
					// kept its address assertion — replace the zombie.
					if dev.ShouldDiscoveryDial(discoveryDialMinInterval) {
						port := tcpPort
						if p := dev.LastPort(); validDialPort(p) {
							port = p
						}
						go DialDevice(ctx, ip, port, body.DeviceID, body.ProtocolVersion, identity, cfg, devices, plugins, localDeviceID, logger, false, opts)
					}
				}
			}
			if opts.Reconnect.SightingDriven {
				// Wake or respawn the parked reconnect loop: the peer is provably
				// back, so the attempt counter restarts and backoff does not apply.
				dev.PokeReconnect()
				if !dev.IsConnected() && dev.TryReconnect() {
					dev.ResetReconnectAttempt()
					go reconnectWithBackoff(ctx, dev, ip, identity, cfg, devices, plugins, localDeviceID, logger, opts)
				}
			}
			return
		}

		safeName := protocol.SanitizeDeviceName(body.DeviceName)
		if !known {
			dev = device.NewDevice(body.DeviceID, safeName, body.DeviceType, logger)
			devices.Add(dev)
		} else {
			dev.SetName(safeName)
		}
		dev.SetDiscoveryAddr(ip, tcpPort)
		dev.SetLastSeen(time.Now())

		if pairingMode || dev.ConsumePairDial() {
			// Spawn goroutine to prevent blocking the discovery listener
			go func(targetIP net.IP, targetPort int, targetID string, targetProto int) {
				DialDevice(ctx, targetIP, targetPort, targetID, targetProto, identity, cfg, devices, plugins, localDeviceID, logger, true, opts)
			}(ip, tcpPort, body.DeviceID, body.ProtocolVersion)
			return
		}

		if !dev.EphemeralDialed() {
			dev.MarkEphemeralDialed()
			if !allowEphemeralDial(ip) {
				return
			}
			// Spawn goroutine to prevent blocking the discovery listener
			go func(targetIP net.IP, targetPort int, targetID string, targetProto int) {
				DialDevice(ctx, targetIP, targetPort, targetID, targetProto, identity, cfg, devices, plugins, localDeviceID, logger, false, opts)
			}(ip, tcpPort, body.DeviceID, body.ProtocolVersion)
		}
		// Already got its ephemeral dial this era: stay silent on further
		// announcements. Pairing mode and explicit pair bypass.
	}

	udpListener := discovery.NewListener(protocol.DefaultDiscoveryPort, localDeviceID, onDeviceFound, logger)
	// Active discovery shares the broadcast ownership lifetime: mDNS browsing
	// runs only while pairing or reconnect owners hold it, never steady state.
	if bc != nil {
		bc.SetBrowseStarter(udpListener.RunMdnsDiscovery)
	}
	go udpListener.Run(ctx)

	// Accept loop
	go func() {
		var acceptBackoff netutil.ErrorBackoff
		for {
			conn, err := tcpListener.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				logger.Warn("accept error", log.Error(err))
				if !acceptBackoff.Wait(ctx) {
					return
				}
				continue
			}
			acceptBackoff.Reset()

			go func(c net.Conn) {
				defer func() {
					if c != nil {
						c.Close()
					}
				}()

				_ = c.SetReadDeadline(time.Now().Add(config.Duration(opts.Network.HandshakeTimeout)))
				preTlsPkt, newConn, err := transport.ReadPlaintextPacket(c)
				if err != nil {
					return
				}
				_ = c.SetReadDeadline(time.Time{})
				// Refuse duplicate bursts inside the cooldown: the peer already
				// has a live session. Pairing traffic always passes through.
				var preBody protocol.IdentityBody
				if err := json.Unmarshal(preTlsPkt.Body, &preBody); err == nil {
					if !protocol.ValidDeviceID(preBody.DeviceID) {
						protocol.ReleasePacket(preTlsPkt)
						return
					}
					if protocol.SanitizeDeviceName(preBody.DeviceName) == "" {
						protocol.ReleasePacket(preTlsPkt)
						return
					}
					if preBody.TargetDeviceID != "" && preBody.TargetDeviceID != localDeviceID {
						logger.Warn("inbound addressed to another device, dropping",
							log.String("device_id", preBody.DeviceID),
							log.String("target_device_id", preBody.TargetDeviceID))
						protocol.ReleasePacket(preTlsPkt)
						return
					}
					pairingMode := bc != nil && bc.IsRunning()
					if shouldRefuseInboundCooldown(preBody, devices, pairingMode) {
						logger.Debug("refusing inbound inside reconnect cooldown",
							log.String("device_id", preBody.DeviceID))
						protocol.ReleasePacket(preTlsPkt)
						return
					}
				} else {
					protocol.ReleasePacket(preTlsPkt)
					return
				}
				protocol.ReleasePacket(preTlsPkt)

				tlsConn := tls.Client(newConn, cfg)
				handshakeCtx, cancel := context.WithTimeout(ctx, config.Duration(opts.Network.HandshakeTimeout))
				defer cancel()
				if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
					return
				}

				transConn := transport.NewConn(tlsConn)
				c = nil // Prevent defer from closing the active connection

				if err := handleNewConnection(ctx, transConn, identity, &preBody, "", devices, plugins, localDeviceID, cfg, logger, opts); err != nil {
					logger.Debug("new connection setup failed", log.Error(err))
					transConn.Close()
				}
			}(conn)
		}
	}()

	<-ctx.Done()
}

func shouldRefuseInboundCooldown(identity protocol.IdentityBody, devices *device.Registry, pairingMode bool) bool {
	if pairingMode {
		return false
	}
	dev, ok := devices.Get(identity.DeviceID)
	return ok && dev.InCooldown()
}
