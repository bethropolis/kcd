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
			if err := handleNewConnection(ctx, transConn, identity, devices, plugins, logger); err != nil {
				logger.Debug("new connection setup failed", zap.Error(err))
				transConn.Close()
			}
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

				if err := handleNewConnection(ctx, transConn, identity, devices, plugins, logger); err != nil {
					logger.Debug("new connection setup failed", zap.Error(err))
					transConn.Close()
				}
			}(conn)
		}
	}()

	<-ctx.Done()
}

// Returning an error ensures the caller can close the connection if it fails mid-setup.
func handleNewConnection(ctx context.Context, conn *transport.Conn, identity *protocol.Packet, devices *device.Registry, plugins *plugin.Registry, logger *zap.Logger) error {
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
	}

	dev.Connect(ctx, conn, dispatch, onConnect, onDisconnect)
	return nil
}
