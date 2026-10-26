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
func handleNewConnection(ctx context.Context, conn *transport.Conn, identity *protocol.Packet, preTLS *protocol.IdentityBody, expectedPeerID string, devices *device.Registry, plugins *plugin.Registry, localDeviceID string, cfg *tls.Config, logger log.Logger, opts *config.Config) error {
	if err := conn.SetReadDeadline(time.Now().Add(config.Duration(opts.Network.HandshakeTimeout))); err != nil {
		return fmt.Errorf("set handshake read deadline: %w", err)
	}
	defer conn.SetReadDeadline(time.Time{})

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
	if preTLS == nil {
		return fmt.Errorf("missing pre-TLS identity")
	}

	var peerBody protocol.IdentityBody
	if err := json.Unmarshal(peerPkt.Body, &peerBody); err != nil {
		return fmt.Errorf("failed to unmarshal peer identity: %w", err)
	}
	if !protocol.ValidDeviceID(peerBody.DeviceID) {
		return fmt.Errorf("invalid device ID %q", peerBody.DeviceID)
	}
	if (preTLS.DeviceID != "" && peerBody.DeviceID != preTLS.DeviceID) ||
		(preTLS.ProtocolVersion > 0 && peerBody.ProtocolVersion != preTLS.ProtocolVersion) {
		return fmt.Errorf("post-TLS identity differs from pre-TLS identity")
	}
	if expectedPeerID != "" && peerBody.DeviceID != expectedPeerID {
		return fmt.Errorf("post-TLS device ID %q does not match target %q", peerBody.DeviceID, expectedPeerID)
	}

	peerCert := conn.PeerCert()
	if peerCert == nil {
		return fmt.Errorf("no peer certificate presented")
	}

	certCN := peerCert.Subject.CommonName
	if !cert.MatchesDeviceID(peerCert, peerBody.DeviceID) {
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
	dev.SetPeerProtocolVersion(peerBody.ProtocolVersion)
	dev.SetLastSeen(time.Now())

	certFP := cert.Fingerprint(peerCert)
	if currentCert := dev.PeerCert(); currentCert != nil && cert.Fingerprint(currentCert) != certFP {
		return fmt.Errorf("certificate fingerprint mismatch on live link (possible MITM)")
	}
	switch dev.State() {
	case device.StatePaired:
		if dev.CertFP != "" && dev.CertFP != certFP {
			return fmt.Errorf("certificate fingerprint mismatch (possible MITM)")
		}
	case device.StatePairRequested, device.StatePairRequestedByPeer:
		if dev.CertFP == "" {
			dev.CertFP = certFP
		} else if dev.CertFP != certFP {
			return fmt.Errorf("certificate fingerprint mismatch during pairing")
		}
	default:
		dev.CertFP = certFP
	}

	// Remember the peer's listening port from the authenticated exchange, so
	// paired auto-dials use it instead of trusting later announcements.
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

	onConnect := func(sender *device.Device) { notifyPluginsConnected(plugins, sender) }

	onDisconnect := func(sender *device.Device) {
		notifyPluginsDisconnected(plugins, sender)
		// Only attempt reconnection for paired devices whose last IP we know.
		// Unpaired or manually-disconnected devices are left alone.
		if sender.State() != device.StatePaired {
			return
		}
		lastIP := sender.LastIP()
		if lastIP == nil {
			return
		}
		// A connection that lived long enough resets the backoff; a quick
		// flap keeps the counter so it escalates instead of hammering.
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

func notifyPluginsConnected(plugins *plugin.Registry, sender *device.Device) {
	if sender.State() == device.StatePaired {
		plugins.OnConnect(sender)
	}
}

func notifyPluginsDisconnected(plugins *plugin.Registry, sender *device.Device) {
	if sender.State() == device.StatePaired {
		plugins.OnDisconnect(sender)
	}
}
