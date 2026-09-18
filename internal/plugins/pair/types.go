package pair

import (
	"crypto/x509"
	"sync"
	"time"

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
