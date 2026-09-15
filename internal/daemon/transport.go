package daemon

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/discovery"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
	"go.uber.org/zap"
)

// reconnectFlapThreshold is the minimum connection lifetime for it to count
// as genuinely stable. A connection that drops sooner is treated as a flap
// (e.g. a peer that keeps dying), so the auto-reconnect backoff continues
// escalating instead of resetting to the 2s floor on every drop.
const reconnectFlapThreshold = 15 * time.Second

// validDialPort reports whether a discovery-advertised TCP port is usable.
// Port 0 and out-of-range values come from malformed or hostile packets and
// must never reach the dialer (port 0 would dial ":0").
func validDialPort(port int) bool {
	return port > 0 && port <= 65535
}

// DialDevice manually connects to a device at the given IP and port.
func DialDevice(ctx context.Context, targetIP net.IP, targetPort int, targetID string, targetProto int, identity *protocol.Packet, cfg *tls.Config, devices *device.Registry, plugins *plugin.Registry, localDeviceID string, logger *zap.Logger) {
	if targetIP == nil || !validDialPort(targetPort) {
		logger.Debug("refusing to dial invalid target",
			zap.String("device_id", targetID),
			zap.String("ip", targetIP.String()),
			zap.Int("port", targetPort))
		return
	}
	addr := fmt.Sprintf("%s:%d", targetIP, targetPort)
	logger.Debug("dialing discovered device", zap.String("device_id", targetID), zap.String("addr", addr))

	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second, // Crucial for detecting dead connections
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		logger.Debug("failed to dial peer", zap.Error(err))
		return
	}

	var myID protocol.IdentityBody
	json.Unmarshal(identity.Body, &myID)
	// Clamp the echoed protocol version: discovery bodies are unauthenticated
	// and a garbage value here would only confuse the peer.
	if targetProto <= 0 || targetProto > protocol.ProtocolVersion {
		targetProto = protocol.ProtocolVersion
	}
	preTlsId := protocol.IdentityBody{
		DeviceID:              myID.DeviceID,
		DeviceName:            myID.DeviceName,
		DeviceType:            myID.DeviceType,
		ProtocolVersion:       myID.ProtocolVersion,
		TCPPort:               myID.TCPPort,
		TargetDeviceID:        targetID,
		TargetProtocolVersion: targetProto,
	}
	preTlsPkt, _ := protocol.NewPacket(protocol.TypeIdentity, preTlsId)

	if err := transport.WritePlaintextPacket(conn, preTlsPkt); err != nil {
		conn.Close()
		return
	}

	// KDE Connect inverts TLS roles: TCP client acts as TLS server
	tlsConn := tls.Server(conn, cfg)
	handshakeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
		tlsConn.Close()
		logger.Debug("tls handshake failed", zap.Error(err))
		return
	}

	transConn := transport.NewConn(tlsConn)
	// Ensure the connection is closed if handleNewConnection fails mid-setup
	if err := handleNewConnection(ctx, transConn, identity, devices, plugins, localDeviceID, cfg, logger); err != nil {
		logger.Debug("new connection setup failed", zap.Error(err))
		transConn.Close()
	}
}

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

func runTransport(ctx context.Context, cfg *tls.Config, bc *discovery.BroadcasterController, identity *protocol.Packet, devices *device.Registry, plugins *plugin.Registry, localDeviceID string, logger *zap.Logger) {
	// TCP Listener
	tcpListener, err := transport.Listen(ctx, ":1716")
	if err != nil {
		logger.Error("failed to start TCP listener", zap.Error(err))
		return
	}
	defer tcpListener.Close()

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
				zap.String("device_id", body.DeviceID),
				zap.Int("port", tcpPort))
			return
		}

		pairingMode := bc != nil && bc.IsRunning()
		dev, known := devices.Get(body.DeviceID)

		if known && dev.IsConnected() {
			// Fresh sighting of a connected device: close it again if it
			// exists only for the discovery handshake.
			if shouldEphemeralClose(dev, pairingMode) {
				logger.Debug("closing ephemeral discovery connection",
					zap.String("device_id", body.DeviceID))
				dev.Disconnect()
			}
			return
		}

		if known && dev.State() == device.StatePaired {
			// A sighting proves the peer is alive at the sighted address,
			// so dial it: whoever answers must present the paired
			// certificate (CN + pinned fingerprint are verified in
			// handleNewConnection) or setup fails. A spoofed sighting can
			// only cost a throttled dial, never a session. The persisted
			// LastIP remains the fallback for a silent (non-broadcasting)
			// peer via the backoff loop.
			dev.SetLastSeen(time.Now())
			if !dev.IsConnected() {
				if dev.ShouldDiscoveryDial(10 * time.Second) {
					go DialDevice(ctx, ip, tcpPort, body.DeviceID, body.ProtocolVersion, identity, cfg, devices, plugins, localDeviceID, logger)
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
				DialDevice(ctx, targetIP, targetPort, targetID, targetProto, identity, cfg, devices, plugins, localDeviceID, logger)
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
				DialDevice(ctx, targetIP, targetPort, targetID, targetProto, identity, cfg, devices, plugins, localDeviceID, logger)
			}(ip, tcpPort, body.DeviceID, body.ProtocolVersion)
		}
		// Otherwise the device already had its ephemeral dial for this
		// unpaired era: stay silent instead of redialling on every
		// announcement. Pairing mode and explicit `kcd pair <id>` bypass.
	}

	udpListener := discovery.NewListener(1716, localDeviceID, onDeviceFound, logger)
	go udpListener.Run(ctx)

	// Accept loop
	go func() {
		for {
			conn, err := tcpListener.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				logger.Error("accept error", zap.Error(err))
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
				protocol.ReleasePacket(preTlsPkt)

				tlsConn := tls.Client(newConn, cfg)
				handshakeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
					return
				}

				transConn := transport.NewConn(tlsConn)
				c = nil // Prevent defer from closing the active connection

				if err := handleNewConnection(ctx, transConn, identity, devices, plugins, localDeviceID, cfg, logger); err != nil {
					logger.Debug("new connection setup failed", zap.Error(err))
					transConn.Close()
				}
			}(conn)
		}
	}()

	<-ctx.Done()
}

// Returning an error ensures the caller can close the connection if it fails mid-setup.
func handleNewConnection(ctx context.Context, conn *transport.Conn, identity *protocol.Packet, devices *device.Registry, plugins *plugin.Registry, localDeviceID string, cfg *tls.Config, logger *zap.Logger) error {
	if err := conn.WritePacket(identity); err != nil {
		return fmt.Errorf("failed to send identity: %w", err)
	}

	peerPkt, err := conn.ReadPacket()
	if err != nil {
		return fmt.Errorf("failed to read peer identity: %w", err)
	}
	defer protocol.ReleasePacket(peerPkt)

	if peerPkt.Type != protocol.TypeIdentity {
		return fmt.Errorf("expected identity packet, got %s", peerPkt.Type)
	}

	var peerBody protocol.IdentityBody
	if err := json.Unmarshal(peerPkt.Body, &peerBody); err != nil {
		return fmt.Errorf("failed to unmarshal peer identity: %w", err)
	}

	peerCert := conn.PeerCert()
	if peerCert == nil {
		return fmt.Errorf("no peer certificate presented")
	}

	certCN := peerCert.Subject.CommonName
	if certCN != peerBody.DeviceID {
		return fmt.Errorf("certificate CN (%s) doesn't match device ID (%s)", certCN, peerBody.DeviceID)
	}

	dev, ok := devices.Get(peerBody.DeviceID)
	safeDeviceName := protocol.SanitizeDeviceName(peerBody.DeviceName)
	if !ok {
		dev = device.NewDevice(peerBody.DeviceID, safeDeviceName, peerBody.DeviceType, logger)
		devices.Add(dev)
	} else {
		dev.SetName(safeDeviceName)
	}
	dev.SetLastSeen(time.Now())

	certFP := cert.Fingerprint(peerCert)
	if dev.State() == device.StatePaired && dev.CertFP != "" {
		if dev.CertFP != certFP {
			return fmt.Errorf("certificate fingerprint mismatch (possible MITM)")
		}
	} else {
		dev.CertFP = certFP
	}

	// Remember the peer's listening port from the authenticated exchange so
	// paired auto-dials use a known-good target instead of trusting future
	// (unauthenticated) discovery announcements.
	if validDialPort(peerBody.TCPPort) {
		dev.SetLastPort(peerBody.TCPPort)
	}

	dev.IncomingCaps = peerBody.IncomingCapabilities
	dev.OutgoingCaps = peerBody.OutgoingCapabilities

	logger.Debug("device connected",
		zap.String("device_id", peerBody.DeviceID),
		zap.String("device_name", safeDeviceName),
		zap.Int("protocol_version", peerBody.ProtocolVersion))

	dispatch := func(ctx context.Context, sender *device.Device, pkt *protocol.Packet) bool {
		return plugins.Dispatch(ctx, sender, pkt)
	}

	onConnect := func(sender *device.Device) {
		plugins.OnConnect(sender)
	}

	onDisconnect := func(sender *device.Device) {
		plugins.OnDisconnect(sender)
		// Only attempt reconnection for paired devices whose last IP we know.
		// Unpaired or manually-disconnected devices are left alone.
		if sender.State() != device.StatePaired {
			return
		}
		lastIP := sender.LastIP()
		if lastIP == nil {
			return
		}
		// A connection that lasted long enough was genuinely stable, so the
		// next drop should start the backoff over. A flap (connection dies
		// shortly after a successful dial, e.g. a dying peer) keeps the
		// counter so the backoff keeps escalating instead of hammering at the
		// 2s floor forever.
		if sender.ConnectionAge() >= reconnectFlapThreshold {
			sender.ResetReconnectAttempt()
		}
		// Prevent multiple concurrent reconnect goroutines for the same device.
		if !sender.TryReconnect() {
			logger.Debug("auto-reconnect: already reconnecting, skipping",
				zap.String("device_id", sender.ID()))
			return
		}
		go reconnectWithBackoff(ctx, sender, lastIP, identity, cfg, devices, plugins, localDeviceID, logger)
	}

	dev.Connect(ctx, conn, dispatch, onConnect, onDisconnect)
	return nil
}

// reconnectWithBackoff dials a paired device after it disconnects, using
// exponential backoff up to 5 minutes between attempts. It stops as soon as:
//   - the device reconnects (IsConnected becomes true), or
//   - the daemon context is cancelled, or
//   - the device is unpaired.
//
// A fresh connection coming in from the phone side (inbound TCP) will set
// IsConnected, causing the loop to exit cleanly without a duplicate dial.
func reconnectWithBackoff(
	ctx context.Context,
	dev *device.Device,
	ip net.IP,
	identity *protocol.Packet,
	cfg *tls.Config,
	devices *device.Registry,
	plugins *plugin.Registry,
	localDeviceID string,
	logger *zap.Logger,
) {
	const maxBackoff = 5 * time.Minute
	attempt := dev.ReconnectAttempt()

	defer dev.ReconnectDone()

	logger.Info("starting auto-reconnect",
		zap.String("device_id", dev.ID()),
		zap.String("device_name", dev.Name()),
		zap.String("ip", ip.String()),
	)

	for {
		// Stop if the daemon is shutting down.
		if ctx.Err() != nil {
			return
		}

		// Stop if the device was unpaired while we were waiting.
		if dev.State() != device.StatePaired {
			logger.Debug("auto-reconnect: device no longer paired, stopping",
				zap.String("device_id", dev.ID()))
			return
		}

		// Stop if the device already reconnected (inbound connection from phone).
		if dev.IsConnected() {
			logger.Debug("auto-reconnect: device already connected, stopping",
				zap.String("device_id", dev.ID()))
			return
		}

		backoff := device.ReconnectBackoff(attempt, maxBackoff)
		logger.Debug("auto-reconnect: waiting before next attempt",
			zap.String("device_id", dev.ID()),
			zap.Int("attempt", attempt+1),
			zap.Duration("backoff", backoff),
		)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		// Re-check after the sleep — the phone may have connected inbound.
		if dev.IsConnected() || dev.State() != device.StatePaired {
			return
		}

		logger.Info("auto-reconnect: dialling",
			zap.String("device_id", dev.ID()),
			zap.String("ip", ip.String()),
			zap.Int("attempt", attempt+1),
		)

		DialDevice(ctx, ip, 1716, dev.ID(), protocol.ProtocolVersion, identity, cfg, devices, plugins, localDeviceID, logger)

		if dev.IsConnected() {
			logger.Info("auto-reconnect: succeeded",
				zap.String("device_id", dev.ID()),
				zap.Int("attempts", attempt+1),
			)
			// Persist the counter before returning: if this connection flaps,
			// the next reconnect cycle continues backing off rather than
			// resetting to the 2s floor. onDisconnect resets it if the
			// connection proved stable.
			dev.SetReconnectAttempt(attempt + 1)
			return
		}

		attempt++
	}
}
