package pair

import (
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// AcceptPairing accepts an incoming pair request.
func (p *PairPlugin) AcceptPairing(dev *device.Device) error {
	pkt, err := protocol.NewPairPacket(protocol.PairAccept, 0)
	if err != nil {
		return err
	}

	if err := dev.Send(pkt); err != nil {
		p.logger.Error("failed to send pair accept", log.Error(err))
		dev.SetState(device.StateUnpaired)
		dev.ClearEphemeral()
		dev.ClearPairDial()
		return err
	}

	p.pairingDone(dev)
	return nil
}

// RequestPairing initiates a pairing request to a device.
func (p *PairPlugin) RequestPairing(dev *device.Device) error {
	if dev.State() == device.StatePaired {
		p.logger.Warn("device already paired", log.String("device_id", dev.ID()))
		return nil
	}

	if dev.State() == device.StatePairRequestedByPeer {
		// They already requested, just accept
		return p.AcceptPairing(dev)
	}

	// The request timestamp seeds the verification code on both sides,
	// so generate it once and send exactly what we store.
	timestamp := time.Now().Unix()
	pkt, err := protocol.NewPairPacket(protocol.PairAccept, timestamp)
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.pairingTimestamp[dev.ID()] = timestamp
	p.mu.Unlock()

	if err := dev.Send(pkt); err != nil {
		p.logger.Error("failed to send pair request", log.Error(err))
		return err
	}

	peerCert := dev.PeerCert()
	if peerCert != nil {
		vKey := cert.VerificationKey(p.localCert, peerCert, timestamp)
		p.logger.Info("pairing verification code",
			log.String("device_id", dev.ID()),
			log.String("code", vKey))
	}

	dev.SetState(device.StatePairRequested)
	p.logger.Info("pair request sent", log.String("device_id", dev.ID()))

	if p.onStateChanged != nil {
		p.onStateChanged()
	}

	return nil
}

// RejectPairing rejects an incoming pair request.
func (p *PairPlugin) RejectPairing(dev *device.Device) error {
	pkt, err := protocol.NewPairPacket(protocol.PairReject, 0)
	if err != nil {
		return err
	}

	dev.Send(pkt) // best effort
	dev.SetState(device.StateUnpaired)
	dev.ClearEphemeral()
	dev.ClearPairDial()

	p.mu.Lock()
	delete(p.pairingTimestamp, dev.ID())
	p.mu.Unlock()

	if p.onStateChanged != nil {
		p.onStateChanged()
	}

	p.logger.Info("pair request rejected", log.String("device_id", dev.ID()))
	return nil
}

// Unpair removes pairing with a device.
func (p *PairPlugin) Unpair(dev *device.Device) error {
	pkt, err := protocol.NewPairPacket(protocol.PairReject, 0)
	if err != nil {
		return err
	}

	dev.Send(pkt) // best effort
	dev.SetState(device.StateUnpaired)
	dev.ClearEphemeral()
	dev.ClearPairDial()

	p.mu.Lock()
	delete(p.pairingTimestamp, dev.ID())
	p.mu.Unlock()

	if p.onStateChanged != nil {
		p.onStateChanged()
	}

	p.logger.Info("device unpaired", log.String("device_id", dev.ID()))
	return nil
}

func (p *PairPlugin) pairingDone(dev *device.Device) {
	dev.SetState(device.StatePaired)
	dev.ClearPairDial()

	p.mu.Lock()
	delete(p.pairingTimestamp, dev.ID())
	p.mu.Unlock()

	if p.onStateChanged != nil {
		p.onStateChanged()
	}

	p.logger.Info("pairing complete", log.String("device_id", dev.ID()))
	p.emit(events.TypePairAccepted, dev, "")
}
