package pair

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

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
		dev.ClearEphemeral()
		dev.ClearPairDial()
		p.emit(events.TypePairRejected, dev, "")

	case device.StatePairRequestedByPeer:
		// They requested, then cancelled
		p.logger.Info("pair request cancelled by peer", zap.String("device_id", dev.ID()))
		dev.SetState(device.StateUnpaired)
		dev.ClearEphemeral()
		dev.ClearPairDial()
		p.emit(events.TypePairRejected, dev, "")

	case device.StatePaired:
		// Unpair request
		p.logger.Info("unpair request received", zap.String("device_id", dev.ID()))
		dev.SetState(device.StateUnpaired)
		dev.ClearEphemeral()
		dev.ClearPairDial()

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
