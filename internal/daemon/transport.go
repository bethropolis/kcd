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
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
)

// shouldEphemeralClose reports whether an active connection that exists
// only for the discovery handshake should be closed on a fresh sighting.
// Connections are kept when pairing mode is active, when the device is
// paired, when a pair request is in flight either way, or when the user
// explicitly asked to pair with the device.
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

// discoveryDialMinInterval rate-limits ephemeral dials triggered by UDP
// sightings of unknown devices: at most one dial per device per interval.
// Without it a chatty announcer would spawn a dial storm.
const discoveryDialMinInterval = 2 * time.Second

func runTransport(ctx context.Context, cfg *tls.Config, bc *discovery.BroadcasterController, identity *protocol.Packet, devices *device.Registry, plugins *plugin.Registry, localDeviceID string, logger log.Logger, opts *config.Config) {

	// TCP Listener
	tcpListener, err := transport.Listen(ctx, fmt.Sprintf(":%d", opts.TCPPort))
	if err != nil {
		logger.Error("failed to start TCP listener", log.Error(err))
		return
	}
	defer tcpListener.Close()
	tcpListener.SetKeepAliveIdle(config.Duration(opts.Network.KeepAliveIdle))

	// Broadcast is off by default — controlled via `kcd pair` or IPC.
	// The controller is started in stopped state.

	// UDP/mDNS Listener (onDeviceFound)
	//
	// Discovery is event-driven with no timers or polling:
	//   - Paired devices, pairing mode (`kcd pair` listen), and explicit
	//     `kcd pair <id>` intent dial and keep the connection.
	//   - An unpaired stranger gets exactly one ephemeral dial per unpaired
	//     era so both sides can list each other (the TCP identity exchange
	//     is what makes the PC appear on the phone). The next sighting
	//     closes the socket again while it is still unpaired.
	// Ephemeral-dial rate limiting: a hostile or buggy peer minting fresh
	// device IDs per broadcast could otherwise spawn an unbounded dial per
	// announcement. Stranger dials are throttled globally (1/s) and per
	// announcer IP (1/5s). Paired and explicit-intent dials bypass this —
	// paired dials have their own per-device throttle below and intent
	// dials are user-initiated.
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
			// Fresh sighting of a connected device: close it again if it
			// exists only for the discovery handshake.
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
			// Paired sighting proves the peer is alive at the sighted
			// address. Redial (rate-limited) when disconnected or when
			// the sighted IP differs (true roam — the live socket is a
			// half-open zombie). A same-IP sighting on a live socket is
			// ignored like upstream: phone traffic is event-driven with
			// long quiet gaps, so read-idle can't tell a zombie from a
			// healthy session, and redialling churns duplicates the peer
			// RSTs (split-second flap). Same-IP roam repair arrives via
			// phone-initiated inbound, which replaces via Connect().
			// Whoever answers must present the paired certificate (CN +
			// pinned fingerprint are verified in handleNewConnection) or
			// setup fails.
			dev.SetLastSeen(time.Now())
			if dev.NoteSighting(ip) {
				dev.ResetReconnectAttempt()
			}
			// Instant dial only when the peer is somewhere new: the
			// backoff loop already covers the last-known address, and the
			// phone redials inbound on its own within a second of a drop.
			// Racing either with a second outbound breeds crossing
			// duplicates the peer RSTs (flap war that latches its UI to
			// "not reachable"). A sighting at a new address is a genuine
			// roam the backoff can't reach — dial it now.
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
				// Wake a parked reconnect loop (peer provably alive at
				// this address), or respawn one that gave up past the
				// stale horizon. TryReconnect single-flights: exactly one
				// loop per device. The sighted address replaces a stale
				// LastIP, and the attempt counter restarts — the peer is
				// provably back, so escalated backoff no longer applies.
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
		// Otherwise the device already had its ephemeral dial for this
		// unpaired era: stay silent instead of redialling on every
		// announcement. Pairing mode and explicit `kcd pair <id>` bypass.
	}

	udpListener := discovery.NewListener(opts.TCPPort, localDeviceID, onDeviceFound, logger)
	// Active discovery shares the broadcast ownership lifetime: mDNS
	// browsing (periodic probes) runs only while pairing or reconnect
	// owners hold the controller, never at connected steady state.
	if bc != nil {
		bc.SetBrowseStarter(udpListener.RunMdnsDiscovery)
	}
	go udpListener.Run(ctx)

	// Accept loop
	go func() {
		for {
			conn, err := tcpListener.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				logger.Error("accept error", log.Error(err))
				continue
			}

			go func(c net.Conn) {
				defer func() {
					if c != nil {
						c.Close()
					}
				}()

				preTlsPkt, newConn, err := transport.ReadPlaintextPacket(c)
				if err != nil {
					return
				}
				// Refuse duplicate bursts pre-TLS: a device that completed a
				// handshake inside the cooldown already has a live session;
				// letting this one through would churn both ends' duplicate
				// resolution. Pairing traffic always passes (strangers have
				// no cooldown entry anyway).
				var preBody protocol.IdentityBody
				if err := json.Unmarshal(preTlsPkt.Body, &preBody); err == nil {
					if preBody.TargetDeviceID != "" && preBody.TargetDeviceID != localDeviceID {
						// Log-only: a stale cached ID on the peer (e.g. after
						// our reinstall minted a new device ID) must not kill
						// an otherwise legitimate inbound.
						logger.Debug("inbound addressed to another device",
							log.String("device_id", preBody.DeviceID),
							log.String("target_device_id", preBody.TargetDeviceID))
					}
					if dev, ok := devices.Get(preBody.DeviceID); ok && dev.InCooldown() &&
						(bc == nil || !bc.IsRunning()) {
						logger.Debug("refusing inbound inside reconnect cooldown",
							log.String("device_id", preBody.DeviceID))
						protocol.ReleasePacket(preTlsPkt)
						return
					}
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

				if err := handleNewConnection(ctx, transConn, identity, devices, plugins, localDeviceID, cfg, logger, opts); err != nil {
					logger.Debug("new connection setup failed", log.Error(err))
					transConn.Close()
				}
			}(conn)
		}
	}()

	<-ctx.Done()
}
