package pair

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

const (
	// AllowedTimestampDiff is the maximum allowed time difference for pairing timestamps (30 min)
	AllowedTimestampDiff = 1800
)

// PairPlugin handles KDE Connect pairing protocol.
type PairPlugin struct {
	devices        *device.Registry
	localCert      *x509.Certificate
	onStateChanged func() // callback to persist state
	logger         *zap.Logger
	bus            *events.Bus
	cfg            config.PairingConfig

	mu               sync.Mutex
	pairingTimestamp map[string]int64 // deviceID -> timestamp from pair request
}

// NewPairPlugin creates a new pairing plugin.
func NewPairPlugin(devices *device.Registry, localCert *x509.Certificate, cfg config.PairingConfig, onStateChanged func(), bus *events.Bus, logger *zap.Logger) *PairPlugin {
	return &PairPlugin{
		devices:          devices,
		localCert:        localCert,
		cfg:              cfg,
		onStateChanged:   onStateChanged,
		logger:           logger.Named("pair"),
		bus:              bus,
		pairingTimestamp: make(map[string]int64),
	}
}

// emit publishes an event to the bus if one is configured.
func (p *PairPlugin) emit(typ events.EventType, dev *device.Device, vKey string) {
	if p.bus == nil {
		return
	}
	payload := map[string]interface{}{
		"name": dev.Name(),
		"type": dev.Type,
	}
	if vKey != "" {
		payload["verificationKey"] = vKey
	}
	p.bus.Publish(typ, dev.ID(), payload)
}

func (p *PairPlugin) Name() string { return "Pair" }

func (p *PairPlugin) Timeout() time.Duration { return 5 * time.Second }

func (p *PairPlugin) IncomingTypes() []string {
	return []string{protocol.TypePair}
}

func (p *PairPlugin) OutgoingTypes() []string {
	return []string{protocol.TypePair}
}

func (p *PairPlugin) OnConnect(dev device.Sender) {}

func (p *PairPlugin) OnDisconnect(dev device.Sender) {}

func (p *PairPlugin) Handle(ctx context.Context, sender device.Sender, pkt *protocol.Packet) error {
	var body protocol.PairBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	dev, ok := p.devices.Get(sender.ID())
	if !ok {
		p.logger.Warn("pair packet from unknown device", zap.String("device_id", sender.ID()))
		return nil
	}

	if body.Pair {
		return p.handlePairRequest(ctx, dev, body)
	}
	return p.handleUnpairRequest(ctx, dev)
}

func (p *PairPlugin) handlePairRequest(_ context.Context, dev *device.Device, body protocol.PairBody) error {
	state := dev.State()

	switch state {
	case device.StatePairRequested:
		// We requested pairing, they accepted
		p.logger.Info("pairing accepted by peer", zap.String("device_id", dev.ID()))
		p.pairingDone(dev)

	case device.StatePairRequestedByPeer:
		// Already have a pending request, ignore duplicate
		p.logger.Debug("ignoring duplicate pair request", zap.String("device_id", dev.ID()))

	case device.StatePaired:
		// Already paired - this is normal behavior in KDE Connect.
		// The peer sends pair:true as confirmation/keep-alive.
		// Just acknowledge by sending pair:true back.
		p.logger.Debug("received pair confirmation from already paired device", zap.String("device_id", dev.ID()))
		pkt, _ := protocol.NewPairPacket(protocol.PairAccept)
		dev.Send(pkt)
		return nil

	case device.StateUnpaired, device.StateUnknown:
		// New pair request from peer
		// Validate timestamp for protocol v8
		if body.Timestamp > 0 {
			now := time.Now().Unix()
			diff := now - body.Timestamp
			if diff < -AllowedTimestampDiff || diff > AllowedTimestampDiff {
				p.logger.Warn("pair request timestamp out of range",
					zap.String("device_id", dev.ID()),
					zap.Int64("timestamp", body.Timestamp),
					zap.Int64("now", now))
				// Send rejection
				pkt, _ := protocol.NewPairPacket(protocol.PairReject)
				dev.Send(pkt)
				return nil
			}
			// Store timestamp for verification key
			p.mu.Lock()
			p.pairingTimestamp[dev.ID()] = body.Timestamp
			p.mu.Unlock()
		}

		p.logger.Info("incoming pair request", zap.String("device_id", dev.ID()))

		var vKey string
		peerCert := dev.PeerCert()
		if peerCert != nil {
			vKey = cert.VerificationKey(p.localCert, peerCert)
			if len(vKey) > 16 {
				vKey = vKey[:16]
			}
			p.logger.Info("pairing verification code",
				zap.String("device_id", dev.ID()),
				zap.String("code", vKey))
		}

		// Set state and wait for user to accept via CLI
		dev.SetState(device.StatePairRequestedByPeer)
		if p.onStateChanged != nil {
			p.onStateChanged()
		}

		p.emit(events.TypePairRequested, dev, vKey)
	}

	return nil
}

func (p *PairPlugin) handleUnpairRequest(_ context.Context, dev *device.Device) error {
	state := dev.State()

	switch state {
	case device.StatePairRequested:
		// We requested, they rejected
		p.logger.Info("pair request rejected by peer", zap.String("device_id", dev.ID()))
		dev.SetState(device.StateUnpaired)
		p.emit(events.TypePairRejected, dev, "")

	case device.StatePairRequestedByPeer:
		// They requested, then cancelled
		p.logger.Info("pair request cancelled by peer", zap.String("device_id", dev.ID()))
		dev.SetState(device.StateUnpaired)
		p.emit(events.TypePairRejected, dev, "")

	case device.StatePaired:
		// Unpair request
		p.logger.Info("unpair request received", zap.String("device_id", dev.ID()))
		dev.SetState(device.StateUnpaired)

	case device.StateUnpaired, device.StateUnknown:
		// Already unpaired, ignore
		p.logger.Debug("ignoring unpair request for unpaired device", zap.String("device_id", dev.ID()))
	}

	// Clean up stored timestamp
	p.mu.Lock()
	delete(p.pairingTimestamp, dev.ID())
	p.mu.Unlock()

	if p.onStateChanged != nil {
		p.onStateChanged()
	}

	return nil
}

// AcceptPairing accepts an incoming pair request.
func (p *PairPlugin) AcceptPairing(dev *device.Device) error {
	pkt, err := protocol.NewPairPacket(protocol.PairAccept)
	if err != nil {
		return err
	}

	if err := dev.Send(pkt); err != nil {
		p.logger.Error("failed to send pair accept", zap.Error(err))
		dev.SetState(device.StateUnpaired)
		return err
	}

	p.pairingDone(dev)
	return nil
}

// RequestPairing initiates a pairing request to a device.
func (p *PairPlugin) RequestPairing(dev *device.Device) error {
	if dev.State() == device.StatePaired {
		p.logger.Warn("device already paired", zap.String("device_id", dev.ID()))
		return nil
	}

	if dev.State() == device.StatePairRequestedByPeer {
		// They already requested, just accept
		return p.AcceptPairing(dev)
	}

	pkt, err := protocol.NewPairPacket(protocol.PairAccept)
	if err != nil {
		return err
	}

	// Store our timestamp
	p.mu.Lock()
	p.pairingTimestamp[dev.ID()] = time.Now().Unix()
	p.mu.Unlock()

	if err := dev.Send(pkt); err != nil {
		p.logger.Error("failed to send pair request", zap.Error(err))
		return err
	}

	peerCert := dev.PeerCert()
	if peerCert != nil {
		vKey := cert.VerificationKey(p.localCert, peerCert)
		if len(vKey) > 16 {
			vKey = vKey[:16]
		}
		p.logger.Info("pairing verification code",
			zap.String("device_id", dev.ID()),
			zap.String("code", vKey))
	}

	dev.SetState(device.StatePairRequested)
	p.logger.Info("pair request sent", zap.String("device_id", dev.ID()))

	if p.onStateChanged != nil {
		p.onStateChanged()
	}

	return nil
}

// RejectPairing rejects an incoming pair request.
func (p *PairPlugin) RejectPairing(dev *device.Device) error {
	pkt, err := protocol.NewPairPacket(protocol.PairReject)
	if err != nil {
		return err
	}

	dev.Send(pkt) // best effort
	dev.SetState(device.StateUnpaired)

	p.mu.Lock()
	delete(p.pairingTimestamp, dev.ID())
	p.mu.Unlock()

	if p.onStateChanged != nil {
		p.onStateChanged()
	}

	p.logger.Info("pair request rejected", zap.String("device_id", dev.ID()))
	return nil
}

// Unpair removes pairing with a device.
func (p *PairPlugin) Unpair(dev *device.Device) error {
	pkt, err := protocol.NewPairPacket(protocol.PairReject)
	if err != nil {
		return err
	}

	dev.Send(pkt) // best effort
	dev.SetState(device.StateUnpaired)

	p.mu.Lock()
	delete(p.pairingTimestamp, dev.ID())
	p.mu.Unlock()

	if p.onStateChanged != nil {
		p.onStateChanged()
	}

	p.logger.Info("device unpaired", zap.String("device_id", dev.ID()))
	return nil
}

func (p *PairPlugin) pairingDone(dev *device.Device) {
	dev.SetState(device.StatePaired)

	p.mu.Lock()
	delete(p.pairingTimestamp, dev.ID())
	p.mu.Unlock()

	if p.onStateChanged != nil {
		p.onStateChanged()
	}

	p.logger.Info("pairing complete", zap.String("device_id", dev.ID()))
	p.emit(events.TypePairAccepted, dev, "")
}
