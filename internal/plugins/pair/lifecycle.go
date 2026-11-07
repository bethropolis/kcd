package pair

import (
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

const (
	// Older configs with a zero timeout inherit the config's documented default.
	defaultPendingPairTimeout = 30 * time.Second
	// Android's requester stops waiting after 30 seconds.
	androidOutgoingPairWait = 30 * time.Second
	// Android stops accepting a request after 25 seconds, even if kcd waits longer.
	androidIncomingPairWait = 25 * time.Second
)

func (p *PairPlugin) changeState(dev *device.Device, next device.PairingState) {
	for {
		p.mu.Lock()
		previous := dev.State()
		if previous == next {
			p.mu.Unlock()
			return
		}
		if dev.TransitionState(previous, next) {
			p.updatePendingTimerLocked(dev, next)
			p.mu.Unlock()
			p.notifyStateChanged(dev, previous, next)
			return
		}
		p.mu.Unlock()
	}
}

func (p *PairPlugin) changeStateFrom(dev *device.Device, expected, next device.PairingState) {
	p.mu.Lock()
	if !dev.TransitionState(expected, next) {
		p.mu.Unlock()
		return
	}
	p.updatePendingTimerLocked(dev, next)
	p.mu.Unlock()
	p.notifyStateChanged(dev, expected, next)
}

func (p *PairPlugin) notifyStateChanged(dev *device.Device, previous, next device.PairingState) {
	if p.onStateChanged != nil {
		p.onStateChanged()
	}
	if previous == device.StatePaired && next != device.StatePaired && p.onUnpaired != nil {
		p.onUnpaired(dev)
	}
	if previous != device.StatePaired && next == device.StatePaired && p.onPaired != nil {
		p.onPaired(dev)
	}
}

func (p *PairPlugin) updatePendingTimerLocked(dev *device.Device, next device.PairingState) {
	if timer := p.pendingTimers[dev.ID()]; timer != nil {
		timer.Stop()
	}
	delete(p.pendingTimers, dev.ID())
	delete(p.pendingGeneration, dev.ID())
	if next == device.StatePairRequested || next == device.StatePairRequestedByPeer {
		p.schedulePendingTimerLocked(dev.ID(), next)
	}
}

func (p *PairPlugin) schedulePendingTimerLocked(deviceID string, expected device.PairingState) {
	delay := p.pendingTimeout(expected)
	p.nextGeneration++
	generation := p.nextGeneration
	p.pendingGeneration[deviceID] = generation
	p.pendingTimers[deviceID] = time.AfterFunc(delay, func() {
		p.expirePending(deviceID, expected, generation)
	})
}

func (p *PairPlugin) pendingTimeout(state device.PairingState) time.Duration {
	seconds := int64(p.cfg.TimeoutSecs)
	if seconds <= 0 {
		return defaultPendingPairTimeout
	}
	maxSeconds := int64((1<<63 - 1) / int64(time.Second))
	if seconds > maxSeconds {
		seconds = maxSeconds
	}
	delay := time.Duration(seconds) * time.Second
	if state == device.StatePairRequested && delay > androidOutgoingPairWait {
		return androidOutgoingPairWait
	}
	if state == device.StatePairRequestedByPeer && delay > androidIncomingPairWait {
		return androidIncomingPairWait
	}
	return delay
}

func (p *PairPlugin) cancelPendingTimer(deviceID string) {
	p.mu.Lock()
	p.cancelPendingTimerLocked(deviceID)
	p.mu.Unlock()
}

func (p *PairPlugin) cancelPendingTimerLocked(deviceID string) {
	if timer := p.pendingTimers[deviceID]; timer != nil {
		timer.Stop()
	}
	delete(p.pendingTimers, deviceID)
	delete(p.pendingGeneration, deviceID)
}

func (p *PairPlugin) expirePending(deviceID string, expected device.PairingState, generation uint64) {
	p.mu.Lock()
	if p.pendingGeneration[deviceID] != generation {
		p.mu.Unlock()
		return
	}
	dev, ok := p.devices.Get(deviceID)
	if !ok || !dev.TransitionState(expected, device.StateUnpaired) {
		p.cancelPendingTimerLocked(deviceID)
		p.mu.Unlock()
		return
	}
	delete(p.pendingTimers, deviceID)
	delete(p.pendingGeneration, deviceID)
	delete(p.pairingTimestamp, deviceID)
	p.mu.Unlock()

	p.notifyStateChanged(dev, expected, device.StateUnpaired)
	dev.ClearEphemeral()
	dev.ClearPairDial()
	p.sendPairReject(dev)
	p.emitRejected(dev, "timeout")
	p.logger.Info("pairing request timed out", log.String("device_id", deviceID))
}

func (p *PairPlugin) transitionPendingToPaired(dev *device.Device) bool {
	p.mu.Lock()
	previous := dev.State()
	if previous != device.StatePairRequested && previous != device.StatePairRequestedByPeer {
		p.mu.Unlock()
		return false
	}
	if !dev.TransitionState(previous, device.StatePaired) {
		p.mu.Unlock()
		return false
	}
	p.cancelPendingTimerLocked(dev.ID())
	p.mu.Unlock()
	p.notifyStateChanged(dev, previous, device.StatePaired)
	return true
}

func (p *PairPlugin) stopPendingTimeout(dev *device.Device, expected device.PairingState) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if dev.State() != expected {
		return false
	}
	p.cancelPendingTimerLocked(dev.ID())
	return true
}

func (p *PairPlugin) sendPairReject(dev *device.Device) {
	pkt, err := protocol.NewPairPacket(protocol.PairReject, 0)
	if err != nil {
		p.logger.Error("failed to create pair rejection", log.Error(err))
		return
	}
	if err := dev.Send(pkt); err != nil {
		p.logger.Warn("failed to send pair rejection", log.String("device_id", dev.ID()), log.Error(err))
	}
}
