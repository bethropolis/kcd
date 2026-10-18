package pair

import (
	"fmt"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// AcceptPairing accepts an incoming pair request.
func (p *PairPlugin) AcceptPairing(dev *device.Device) error {
	state := dev.State()
	if state != device.StatePairRequestedByPeer && state != device.StatePairRequested {
		return fmt.Errorf("pair: no pending request for device %s", dev.ID())
	}
	pkt, err := protocol.NewPairPacket(protocol.PairAccept, 0)
	if err != nil {
		return err
	}
	if !p.stopPendingTimeout(dev, state) {
		return fmt.Errorf("pair: request expired for device %s", dev.ID())
	}

	if err := dev.Send(pkt); err != nil {
		p.logger.Error("failed to send pair accept", log.Error(err))
		p.changeStateFrom(dev, state, device.StateUnpaired)
		dev.ClearEphemeral()
		dev.ClearPairDial()
		return err
	}

	if !p.pairingDone(dev) {
		return fmt.Errorf("pair: request expired for device %s", dev.ID())
	}
	return nil
}

// RequestPairing initiates pairing and returns the peer-displayed code;
// empty when already paired or accepting the peer's pending request.
func (p *PairPlugin) RequestPairing(dev *device.Device) (string, error) {
	if dev.State() == device.StatePaired {
		p.logger.Warn("device already paired", log.String("device_id", dev.ID()))
		return "", nil
	}

	if dev.State() == device.StatePairRequestedByPeer {
		// They already requested, just accept
		return "", p.AcceptPairing(dev)
	}

	// The request timestamp seeds the verification code on both sides,
	// so generate it once and send exactly what we store.
	timestamp := time.Now().Unix()
	pkt, err := protocol.NewPairPacket(protocol.PairAccept, timestamp)
	if err != nil {
		return "", err
	}

	p.mu.Lock()
	p.pairingTimestamp[dev.ID()] = timestamp
	p.mu.Unlock()

	if err := dev.Send(pkt); err != nil {
		p.logger.Error("failed to send pair request", log.Error(err))
		return "", err
	}

	// Without a peer certificate (unauthenticated legacy device) there is
	// no code to compare; the caller surfaces an empty string.
	var verificationKey string
	if peerCert := dev.PeerCert(); peerCert != nil {
		verificationKey = cert.VerificationKey(p.localCert, peerCert, timestamp)
		p.logger.Info("pairing verification code",
			log.String("device_id", dev.ID()),
			log.String("code", verificationKey))
	}

	p.changeState(dev, device.StatePairRequested)
	p.logger.Info("pair request sent", log.String("device_id", dev.ID()))

	return verificationKey, nil
}

// RejectPairing rejects an incoming pair request.
func (p *PairPlugin) RejectPairing(dev *device.Device) error {
	pkt, err := protocol.NewPairPacket(protocol.PairReject, 0)
	if err != nil {
		return err
	}

	dev.Send(pkt) // best effort
	p.changeState(dev, device.StateUnpaired)
	dev.ClearEphemeral()
	dev.ClearPairDial()

	p.mu.Lock()
	delete(p.pairingTimestamp, dev.ID())
	p.mu.Unlock()

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
	p.changeState(dev, device.StateUnpaired)
	dev.ClearEphemeral()
	dev.ClearPairDial()

	p.mu.Lock()
	delete(p.pairingTimestamp, dev.ID())
	p.mu.Unlock()

	p.logger.Info("device unpaired", log.String("device_id", dev.ID()))
	return nil
}

func (p *PairPlugin) pairingDone(dev *device.Device) bool {
	if !p.transitionPendingToPaired(dev) {
		return false
	}
	dev.ClearPairDial()

	p.mu.Lock()
	delete(p.pairingTimestamp, dev.ID())
	p.mu.Unlock()

	p.logger.Info("pairing complete", log.String("device_id", dev.ID()))
	p.emit(events.TypePairAccepted, dev, "")
	return true
}
