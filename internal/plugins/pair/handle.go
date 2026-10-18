package pair

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

func (p *PairPlugin) Handle(ctx context.Context, sender device.Sender, pkt *protocol.Packet) error {
	var body protocol.PairBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	dev, ok := p.devices.Get(sender.ID())
	if !ok {
		p.logger.Warn("pair packet from unknown device", log.String("device_id", sender.ID()))
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
		p.logger.Info("pairing accepted by peer", log.String("device_id", dev.ID()))
		p.pairingDone(dev)
		return nil

	case device.StatePairRequestedByPeer:
		p.logger.Debug("ignoring duplicate pair request", log.String("device_id", dev.ID()))
		return nil

	case device.StatePaired, device.StateUnpaired, device.StateUnknown:
		if !p.validateRequestTimestamp(dev, body.Timestamp) {
			return nil
		}
		p.startIncomingRequest(dev)
	}

	return nil
}

func (p *PairPlugin) validateRequestTimestamp(dev *device.Device, timestamp int64) bool {
	if timestamp <= 0 && dev.PeerProtocolVersion() >= protocol.ProtocolVersion {
		p.logger.Warn("pair request missing timestamp", log.String("device_id", dev.ID()))
		p.sendPairReject(dev)
		return false
	}
	if timestamp <= 0 {
		return true
	}
	now := time.Now().Unix()
	diff := now - timestamp
	if diff < -AllowedTimestampDiff || diff > AllowedTimestampDiff {
		p.logger.Warn("pair request timestamp out of range",
			log.String("device_id", dev.ID()),
			log.Int64("timestamp", timestamp),
			log.Int64("now", now))
		p.sendPairReject(dev)
		return false
	}
	p.mu.Lock()
	p.pairingTimestamp[dev.ID()] = timestamp
	p.mu.Unlock()
	return true
}

func (p *PairPlugin) startIncomingRequest(dev *device.Device) {
	p.logger.Info("incoming pair request", log.String("device_id", dev.ID()))

	var vKey string
	if peerCert := dev.PeerCert(); peerCert != nil {
		vKey = cert.VerificationKey(p.localCert, peerCert, p.pairingTimestampFor(dev.ID()))
		p.logger.Info("pairing verification code",
			log.String("device_id", dev.ID()),
			log.String("code", vKey))
	}

	p.changeState(dev, device.StatePairRequestedByPeer)
	p.emit(events.TypePairRequested, dev, vKey)
}

func (p *PairPlugin) handleUnpairRequest(_ context.Context, dev *device.Device) error {
	state := dev.State()

	switch state {
	case device.StatePairRequested:
		p.logger.Info("pair request rejected by peer", log.String("device_id", dev.ID()))
		p.changeState(dev, device.StateUnpaired)
		dev.ClearEphemeral()
		dev.ClearPairDial()
		p.emitRejected(dev, "")

	case device.StatePairRequestedByPeer:
		p.logger.Info("pair request cancelled by peer", log.String("device_id", dev.ID()))
		p.changeState(dev, device.StateUnpaired)
		dev.ClearEphemeral()
		dev.ClearPairDial()
		p.emitRejected(dev, "")

	case device.StatePaired:
		p.logger.Info("unpair request received", log.String("device_id", dev.ID()))
		p.changeState(dev, device.StateUnpaired)
		dev.ClearEphemeral()
		dev.ClearPairDial()

	case device.StateUnpaired, device.StateUnknown:
		p.logger.Debug("ignoring unpair request for unpaired device", log.String("device_id", dev.ID()))
	}

	// Clean up stored timestamp
	p.mu.Lock()
	delete(p.pairingTimestamp, dev.ID())
	p.mu.Unlock()

	return nil
}
