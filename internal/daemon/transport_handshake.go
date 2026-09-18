package daemon

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
)

// Returning an error ensures the caller can close the connection if it fails mid-setup.
func handleNewConnection(ctx context.Context, conn *transport.Conn, identity *protocol.Packet, devices *device.Registry, plugins *plugin.Registry, localDeviceID string, cfg *tls.Config, logger log.Logger, opts *config.Config) error {
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
		log.String("device_id", peerBody.DeviceID),
		log.String("device_name", safeDeviceName),
		log.Int("protocol_version", peerBody.ProtocolVersion))

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
		if sender.ConnectionAge() >= config.Duration(opts.Reconnect.FlapThreshold) {
			sender.ResetReconnectAttempt()
		}
		// Prevent multiple concurrent reconnect goroutines for the same device.
		if !sender.TryReconnect() {
			logger.Debug("auto-reconnect: already reconnecting, skipping",
				log.String("device_id", sender.ID()))
			return
		}
		go reconnectWithBackoff(ctx, sender, lastIP, identity, cfg, devices, plugins, localDeviceID, logger, opts)
	}

	dev.Connect(ctx, conn, dispatch, onConnect, onDisconnect)
	return nil
}
