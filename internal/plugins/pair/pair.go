package pair

import (
	"crypto/x509"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
)

var _ plugin.Plugin = (*PairPlugin)(nil)

// NewPairPlugin creates a new pairing plugin.
func NewPairPlugin(devices *device.Registry, localCert *x509.Certificate, cfg config.PairingConfig, onStateChanged func(), bus *events.Bus, logger log.Logger) *PairPlugin {
	return &PairPlugin{
		devices:           devices,
		localCert:         localCert,
		cfg:               cfg,
		onStateChanged:    onStateChanged,
		logger:            logger.Named("pair"),
		bus:               bus,
		pairingTimestamp:  make(map[string]int64),
		pendingTimers:     make(map[string]*time.Timer),
		pendingGeneration: make(map[string]uint64),
	}
}

func (p *PairPlugin) Name() string { return "Pair" }

func (p *PairPlugin) Timeout() time.Duration { return 5 * time.Second }

func (p *PairPlugin) IncomingTypes() []string {
	return []string{protocol.TypePair}
}

func (p *PairPlugin) OutgoingTypes() []string {
	return []string{protocol.TypePair}
}

func (p *PairPlugin) OnConnect(dev device.Peer) {}

func (p *PairPlugin) OnDisconnect(dev device.Peer) {}
