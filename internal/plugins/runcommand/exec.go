package runcommand

import (
	"context"

	"github.com/bethropolis/kcd/internal/log"
)

// nextExecID hands out execution ids as a counter: the phone's getInt would
// overflow on nanosecond timestamps.
func (p *RunCommandPlugin) nextExecID() int32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.execSeq++
	return p.execSeq
}

// stopRunning cancels a running execution, if the phone asked for it. The
// phone's stop button sends kdeconnect.runcommand.request {"stop": true}.
func (p *RunCommandPlugin) stopRunning(deviceID string, id int32) {
	p.mu.Lock()
	cancel := p.running[deviceID][id]
	p.mu.Unlock()

	if cancel == nil {
		p.logger.Debug("runcommand: stop for unknown execution",
			log.String("device_id", deviceID), log.Int("id", int(id)))
		return
	}
	p.logger.Info("runcommand: stopping execution on request",
		log.String("device_id", deviceID), log.Int("id", int(id)))
	cancel()
}

// stopAll cancels everything running for a device, used when it disconnects.
func (p *RunCommandPlugin) stopAll(deviceID string) {
	p.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(p.running[deviceID]))
	for _, cancel := range p.running[deviceID] {
		cancels = append(cancels, cancel)
	}
	delete(p.running, deviceID)
	p.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
}
