package daemon

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
)

// validDialPort reports whether an advertised TCP port is dialable; 0 and
// out-of-range come from malformed or hostile packets (":0" must never dial).
func validDialPort(port int) bool {
	return port > 0 && port <= 65535
}

// DialDevice connects to a device at IP:port, skipping dials inside the
// cooldown unless forced; explicit user actions pass force=true.
func DialDevice(ctx context.Context, targetIP net.IP, targetPort int, targetID string, targetProto int, identity *protocol.Packet, cfg *tls.Config, devices *device.Registry, plugins *plugin.Registry, localDeviceID string, logger log.Logger, force bool, opts *config.Config) {
	if targetIP == nil || !validDialPort(targetPort) {
		logger.Debug("refusing to dial invalid target",
			log.String("device_id", targetID),
			log.String("ip", targetIP.String()),
			log.Int("port", targetPort))
		return
	}
	if !force {
		if dev, ok := devices.Get(targetID); ok && dev.InCooldown() {
			logger.Debug("skipping dial inside reconnect cooldown",
				log.String("device_id", targetID),
				log.String("ip", targetIP.String()))
			return
		}
	}
	addr := fmt.Sprintf("%s:%d", targetIP, targetPort)
	logger.Debug("dialing discovered device", log.String("device_id", targetID), log.String("addr", addr))

	dialer := &net.Dialer{
		Timeout: config.Duration(opts.Network.DialTimeout),
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		logger.Debug("failed to dial peer", log.Error(err))
		return
	}
	transport.SetTCPKeepAlive(conn, config.Duration(opts.Network.KeepAliveIdle))

	var myID protocol.IdentityBody
	json.Unmarshal(identity.Body, &myID)
	// Clamp the echoed protocol version: discovery bodies are unauthenticated
	// and a garbage value here would only confuse the peer.
	if targetProto <= 0 || targetProto > protocol.ProtocolVersion {
		targetProto = protocol.ProtocolVersion
	}
	// TargetDeviceID stays empty when the target is unknown (connect-by-IP):
	// stock peers close pre-TLS identities addressed to anyone else.
	preTlsId := protocol.IdentityBody{
		DeviceID:              myID.DeviceID,
		DeviceName:            myID.DeviceName,
		DeviceType:            myID.DeviceType,
		ProtocolVersion:       myID.ProtocolVersion,
		TCPPort:               myID.TCPPort,
		TargetDeviceID:        targetID,
		TargetProtocolVersion: protocol.FlexInt(targetProto),
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
		logger.Debug("tls handshake failed", log.Error(err))
		return
	}

	transConn := transport.NewConn(tlsConn)
	// Ensure the connection is closed if handleNewConnection fails mid-setup
	if err := handleNewConnection(ctx, transConn, identity, devices, plugins, localDeviceID, cfg, logger, opts); err != nil {
		logger.Debug("new connection setup failed", log.Error(err))
		transConn.Close()
	}
}
