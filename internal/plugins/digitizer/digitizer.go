package digitizer

import (
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
)

var _ plugin.Plugin = (*DigitizerPlugin)(nil)

// NewDigitizerPlugin creates a new DigitizerPlugin.
func NewDigitizerPlugin(bus *events.Bus, logger log.Logger, creators ...TabletCreator) *DigitizerPlugin {
	creator := defaultTabletCreator
	if len(creators) > 0 && creators[0] != nil {
		creator = creators[0]
	}
	return &DigitizerPlugin{
		bus:      bus,
		logger:   logger.With(log.String("plugin", "digitizer")),
		creator:  creator,
		sessions: make(map[string]TabletDevice),
	}
}

func (p *DigitizerPlugin) Name() string           { return "Digitizer" }
func (p *DigitizerPlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *DigitizerPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.digitizer.session", "kdeconnect.digitizer"}
}
func (p *DigitizerPlugin) OutgoingTypes() []string { return []string{} }

func (p *DigitizerPlugin) OnConnect(_ device.Peer) {}

func (p *DigitizerPlugin) OnDisconnect(dev device.Peer) {
	p.mu.Lock()
	session, ok := p.sessions[dev.ID()]
	if ok {
		delete(p.sessions, dev.ID())
	}
	p.mu.Unlock()

	if ok && session != nil {
		_ = session.Close()
		p.logger.Info("digitizer: session ended on peer disconnect", log.String("device_id", dev.ID()))
		if p.bus != nil {
			p.bus.Publish(events.TypeDigitizerSession, dev.ID(), map[string]any{"active": false})
		}
	}
}
