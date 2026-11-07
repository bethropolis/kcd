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
func DialDevice(ctx context.Context, targetIP net.IP, targetPort int, targetID string, targetProto int, identity *protocol.Packet, tlsCfg *tls.Config, devices *device.Registry, plugins *plugin.Registry, localDeviceID string, logger log.Logger, force bool, cfg *config.Config) {
	env := &transportEnv{
		tls:      tlsCfg,
		identity: identity,
		devices:  devices,
		plugins:  plugins,
		localID:  localDeviceID,
		cfg:      cfg,
		logger:   logger,
	}
	env.Dial(ctx, targetIP, targetPort, targetID, targetProto, force)
}

func (env *transportEnv) Dial(ctx context.Context, targetIP net.IP, targetPort int, targetID string, targetProto int, force bool) {
	if targetIP == nil || !validDialPort(targetPort) {
		env.logger.Debug("refusing to dial invalid target",
			log.String("device_id", targetID),
			log.String("ip", targetIP.String()),
			log.Int("port", targetPort))
		return
	}
	if !force {
		if dev, ok := env.devices.Get(targetID); ok && dev.InCooldown() {
			env.logger.Debug("skipping dial inside reconnect cooldown",
				log.String("device_id", targetID),
				log.String("ip", targetIP.String()))
			return
		}
	}
	addr := fmt.Sprintf("%s:%d", targetIP, targetPort)
	env.logger.Debug("dialing discovered device", log.String("device_id", targetID), log.String("addr", addr))

	dialer := &net.Dialer{
		Timeout: config.Duration(env.cfg.Network.DialTimeout),
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		env.logger.Debug("failed to dial peer", log.Error(err))
		return
	}
	transport.SetTCPKeepAlive(conn, config.Duration(env.cfg.Network.KeepAliveIdle))

	var myID protocol.IdentityBody
	json.Unmarshal(env.identity.Body, &myID)
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
	_ = conn.SetReadDeadline(time.Now().Add(config.Duration(env.cfg.Network.HandshakeTimeout)))

	// KDE Connect inverts TLS roles: TCP client acts as TLS server
	tlsConn := tls.Server(conn, env.tls)
	handshakeCtx, cancel := context.WithTimeout(ctx, config.Duration(env.cfg.Network.HandshakeTimeout))
	defer cancel()
	if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
		tlsConn.Close()
		env.logger.Debug("tls handshake failed", log.Error(err))
		return
	}

	transConn := transport.NewConn(tlsConn)
	// Ensure the connection is closed if handleNewConnection fails mid-setup
	peerPreTLS := &protocol.IdentityBody{DeviceID: targetID, ProtocolVersion: targetProto}
	if err := env.HandleNewConnection(ctx, transConn, peerPreTLS, targetID); err != nil {
		env.logger.Debug("new connection setup failed", log.Error(err))
		transConn.Close()
	}
}
