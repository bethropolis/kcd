package daemon

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
	"go.uber.org/zap"
)

// validDialPort reports whether a discovery-advertised TCP port is usable.
// Port 0 and out-of-range values come from malformed or hostile packets and
// must never reach the dialer (port 0 would dial ":0").
func validDialPort(port int) bool {
	return port > 0 && port <= 65535
}

// DialDevice connects to a device at IP:port. Unless force is set, dials
// inside reconnectCooldown are skipped so post-roam bursts can't complete
// near-simultaneously and churn the peer's duplicate resolution.
// Explicit user actions (pair intent, manual connect) pass force=true.
func DialDevice(ctx context.Context, targetIP net.IP, targetPort int, targetID string, targetProto int, identity *protocol.Packet, cfg *tls.Config, devices *device.Registry, plugins *plugin.Registry, localDeviceID string, logger *zap.Logger, force bool, opts *config.Config) {
	if targetIP == nil || !validDialPort(targetPort) {
		logger.Debug("refusing to dial invalid target",
			zap.String("device_id", targetID),
			zap.String("ip", targetIP.String()),
			zap.Int("port", targetPort))
		return
	}
	if !force {
		if dev, ok := devices.Get(targetID); ok && dev.InCooldown() {
			logger.Debug("skipping dial inside reconnect cooldown",
				zap.String("device_id", targetID),
				zap.String("ip", targetIP.String()))
			return
		}
	}
	addr := fmt.Sprintf("%s:%d", targetIP, targetPort)
	logger.Debug("dialing discovered device", zap.String("device_id", targetID), zap.String("addr", addr))

	dialer := &net.Dialer{
		Timeout: config.Duration(opts.Network.DialTimeout),
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		logger.Debug("failed to dial peer", zap.Error(err))
		return
	}
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		if err := tcpConn.SetKeepAliveConfig(net.KeepAliveConfig{
			Enable:   true,
			Idle:     30 * time.Second,
			Interval: 10 * time.Second,
			Count:    3,
		}); err != nil {
			_ = tcpConn.SetKeepAlive(true)
			_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
		}
	}

	var myID protocol.IdentityBody
	json.Unmarshal(identity.Body, &myID)
	// Clamp the echoed protocol version: discovery bodies are unauthenticated
	// and a garbage value here would only confuse the peer.
	if targetProto <= 0 || targetProto > protocol.ProtocolVersion {
		targetProto = protocol.ProtocolVersion
	}
	// TargetDeviceID stays empty (and thus absent on the wire) when the
	// target is unknown, e.g. an explicit connect-by-IP: stock peers
	// close pre-TLS identities addressed to any other device ID.
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
	handshakeCtx, cancel := context.WithTimeout(ctx, config.Duration(opts.Network.HandshakeTimeout))
	defer cancel()
	if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
		tlsConn.Close()
		logger.Debug("tls handshake failed", zap.Error(err))
		return
	}

	transConn := transport.NewConn(tlsConn)
	// Ensure the connection is closed if handleNewConnection fails mid-setup
	if err := handleNewConnection(ctx, transConn, identity, devices, plugins, localDeviceID, cfg, logger, opts); err != nil {
		logger.Debug("new connection setup failed", zap.Error(err))
		transConn.Close()
	}
}
