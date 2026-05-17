package daemon

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/discovery"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
	"go.uber.org/zap"
)

// DialDevice manually connects to a device at the given IP and port.
func DialDevice(ctx context.Context, targetIP net.IP, targetPort int, targetID string, targetProto int, identity *protocol.Packet, cfg *tls.Config, devices *device.Registry, plugins *plugin.Registry, localDeviceID string, logger *zap.Logger) {
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

func runTransport(ctx context.Context, cfg *tls.Config, bc *discovery.BroadcasterController, identity *protocol.Packet, devices *device.Registry, plugins *plugin.Registry, localDeviceID string, logger *zap.Logger) {
	// TCP Listener
	tcpListener, err := transport.Listen(":1716")
	if err != nil {
		logger.Error("failed to start TCP listener", zap.Error(err))
		return
	}
	defer tcpListener.Close()

	// Broadcast is off by default — controlled via `kcd pair` or IPC.
	// The controller is started in stopped state.

	// UDP/mDNS Listener (onDeviceFound)
	onDeviceFound := func(ip net.IP, tcpPort int, peerIdentity *protocol.Packet) {
		var body protocol.IdentityBody
		if err := json.Unmarshal(peerIdentity.Body, &body); err != nil {
			return
		}

		if body.DeviceID == localDeviceID {
			return
		}

		if dev, ok := devices.Get(body.DeviceID); ok && dev.IsConnected() {
			return
		}

		// Spawn goroutine to prevent blocking the discovery listener
		go func(targetIP net.IP, targetPort int, targetID string, targetProto int) {
			DialDevice(ctx, targetIP, targetPort, targetID, targetProto, identity, cfg, devices, plugins, localDeviceID, logger)
		}(ip, tcpPort, body.DeviceID, body.ProtocolVersion)
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

	certFP := cert.Fingerprint(peerCert)
	if dev.State() == device.StatePaired && dev.CertFP != "" {
		if dev.CertFP != certFP {
			return fmt.Errorf("certificate fingerprint mismatch (possible MITM)")
		}
	} else {
		dev.CertFP = certFP
	}

	dev.IncomingCaps = peerBody.IncomingCapabilities
	dev.OutgoingCaps = peerBody.OutgoingCapabilities

	logger.Debug("device connected",
		zap.String("device_id", peerBody.DeviceID),
		zap.String("device_name", safeDeviceName),
		zap.Int("protocol_version", peerBody.ProtocolVersion))

	dispatch := func(ctx context.Context, sender *device.Device, pkt *protocol.Packet) {
		plugins.Dispatch(ctx, sender, pkt)
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
	attempt := 0

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
			return
		}

		attempt++
	}
}
