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

func runTransport(ctx context.Context, cfg *tls.Config, enableBroadcast bool, identity *protocol.Packet, devices *device.Registry, plugins *plugin.Registry, localDeviceID string, logger *zap.Logger) {
	// TCP Listener
	tcpListener, err := transport.Listen(":1716")
	if err != nil {
		logger.Error("failed to start TCP listener", zap.Error(err))
		return
	}
	defer tcpListener.Close()

	// UDP Broadcaster
	if enableBroadcast {
		broadcaster := discovery.NewBroadcaster(identity, 30*time.Second, logger)
		go broadcaster.Run(ctx, devices.AllPairedDevicesConnected)
	}

	// UDP Listener (onDeviceFound)
	onDeviceFound := func(ip net.IP, tcpPort int, peerIdentity *protocol.Packet) {
		var body protocol.IdentityBody
		if err := json.Unmarshal(peerIdentity.Body, &body); err != nil {
			return
		}

		if body.DeviceID == localDeviceID {
			return
		}

		// Check if already connected
		if dev, ok := devices.Get(body.DeviceID); ok && dev.IsConnected() {
			return
		}

		addr := fmt.Sprintf("%s:%d", ip, tcpPort)
		logger.Debug("dialing discovered device", zap.String("device_id", body.DeviceID), zap.String("addr", addr))

		// Use a goroutine to avoid blocking the UDP listener during slow TCP dials
		go func(targetAddr string, targetID string, targetProto int) {
			dialer := &net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}
			conn, err := dialer.Dial("tcp", targetAddr)
			if err != nil {
				logger.Debug("failed to dial peer", zap.String("device_id", targetID), zap.Error(err))
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
				logger.Debug("failed to write pre-tls identity", zap.String("device_id", targetID), zap.Error(err))
				conn.Close()
				return
			}

			// KDE Connect inverts TLS roles: TCP client acts as TLS server
			tlsConn := tls.Server(conn, cfg)
			// Ensure TLS handshake also has a timeout
			handshakeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
				tlsConn.Close()
				logger.Debug("tls handshake failed", zap.String("device_id", targetID), zap.Error(err))
				return
			}

			transConn := transport.NewConn(tlsConn)
			handleNewConnection(ctx, transConn, identity, devices, plugins, logger)
		}(addr, body.DeviceID, body.ProtocolVersion)
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
					logger.Debug("tcp accept: read plaintext packet failed", zap.Error(err))
					return
				}
				protocol.ReleasePacket(preTlsPkt)

				// KDE Connect inverts TLS roles: TCP server acts as TLS client
				tlsConn := tls.Client(newConn, cfg)
				if err := tlsConn.Handshake(); err != nil {
					logger.Debug("tcp accept: tls handshake failed", zap.Error(err))
					return
				}

				transConn := transport.NewConn(tlsConn)
				c = nil // prevent double close
				handleNewConnection(ctx, transConn, identity, devices, plugins, logger)
			}(conn)
		}
	}()

	<-ctx.Done()
}

func handleNewConnection(ctx context.Context, conn *transport.Conn, identity *protocol.Packet, devices *device.Registry, plugins *plugin.Registry, logger *zap.Logger) {
	// Send our full identity (post-TLS for protocol v8)
	if err := conn.WritePacket(identity); err != nil {
		logger.Debug("failed to send identity", zap.Error(err))
		return
	}

	// Wait for peer identity (post-TLS for protocol v8)
	peerPkt, err := conn.ReadPacket()
	if err != nil {
		logger.Debug("failed to read peer identity", zap.Error(err))
		return
	}
	defer protocol.ReleasePacket(peerPkt)

	if peerPkt.Type != protocol.TypeIdentity {
		logger.Debug("expected identity packet, got something else", zap.String("type", peerPkt.Type))
		return
	}

	var peerBody protocol.IdentityBody
	if err := json.Unmarshal(peerPkt.Body, &peerBody); err != nil {
		logger.Debug("failed to unmarshal peer identity", zap.Error(err))
		return
	}

	// Validate Peer Certificate
	peerCert := conn.PeerCert()
	if peerCert == nil {
		logger.Debug("no peer certificate presented, dropping connection")
		return
	}

	// KDE Connect requires certificate CN to match device ID
	certCN := peerCert.Subject.CommonName
	if certCN != peerBody.DeviceID {
		logger.Warn("certificate CN doesn't match device ID, dropping connection",
			zap.String("cert_cn", certCN),
			zap.String("device_id", peerBody.DeviceID))
		return
	}

	// Update or create device
	dev, ok := devices.Get(peerBody.DeviceID)
	safeDeviceName := protocol.SanitizeDeviceName(peerBody.DeviceName)
	if !ok {
		dev = device.NewDevice(peerBody.DeviceID, safeDeviceName, peerBody.DeviceType, logger)
		devices.Add(dev)
	} else {
		// Update name if changed
		dev.SetName(safeDeviceName)
	}

	// TLS Pinning: verify fingerprint for paired devices
	certFP := cert.Fingerprint(peerCert)
	if dev.State() == device.StatePaired && dev.CertFP != "" {
		if dev.CertFP != certFP {
			logger.Warn("certificate fingerprint mismatch! dropping connection (possible MITM)",
				zap.String("device_id", peerBody.DeviceID),
				zap.String("expected", dev.CertFP),
				zap.String("got", certFP))
			return
		}
	} else {
		// Store it temporarily so the pairing plugin can use it
		dev.CertFP = certFP
	}

	// Update capabilities
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
	}

	dev.Connect(ctx, conn, dispatch, onConnect, onDisconnect)
}
