package pair

import (
	"crypto/x509"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
)

const (
	// AllowedTimestampDiff bounds acceptable pairing clock skew: wide enough
	// for real drift, narrow enough to keep replays useless. 30 min.
	AllowedTimestampDiff = 1800
)

// PairPlugin handles KDE Connect pairing protocol.
type PairPlugin struct {
	devices        *device.Registry
	localCert      *x509.Certificate
	onStateChanged func() // callback to persist state
	onPaired       func(device.Peer)
	onUnpaired     func(device.Peer)
	logger         log.Logger
	bus            *events.Bus
	cfg            config.PairingConfig

	mu                sync.Mutex
	pairingTimestamp  map[string]int64 // deviceID -> timestamp from pair request
	pendingTimers     map[string]*time.Timer
	pendingGeneration map[string]uint64
	nextGeneration    uint64
}

// SetLifecycleCallbacks sets plugin callbacks for pairing and unpairing transitions.
func (p *PairPlugin) SetLifecycleCallbacks(onPaired, onUnpaired func(device.Peer)) {
	p.onPaired = onPaired
	p.onUnpaired = onUnpaired
}

// pairingTimestampFor returns the stored pair-request timestamp for a
// device, or zero if none was recorded (pre-v8 peer or unknown).
func (p *PairPlugin) pairingTimestampFor(deviceID string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pairingTimestamp[deviceID]
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

func (p *PairPlugin) emitRejected(dev *device.Device, reason string) {
	if p.bus == nil {
		return
	}
	payload := map[string]interface{}{
		"name": dev.Name(),
		"type": dev.Type,
	}
	if reason != "" {
		payload["reason"] = reason
	}
	p.bus.Publish(events.TypePairRejected, dev.ID(), payload)
}
