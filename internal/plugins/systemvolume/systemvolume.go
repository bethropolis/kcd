package systemvolume

import (
	"time"

	"github.com/bethropolis/kcd/internal/desktop/audio"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
)

var _ plugin.Plugin = (*SystemVolumePlugin)(nil)

func NewSystemVolumePlugin(bus *events.Bus, logger log.Logger, backends ...*audio.Backend) *SystemVolumePlugin {
	p := &SystemVolumePlugin{
		logger: logger.With(log.String("plugin", "systemvolume")),
		bus:    bus,
	}
	if len(backends) > 0 && backends[0] != nil {
		p.audio = backends[0]
	} else {
		p.audio = audio.New(nil)
	}
	if !p.audio.Available() {
		p.logger.Warn("systemvolume: no audio backend found (wpctl or pactl required)")
	}
	return p
}

func (p *SystemVolumePlugin) Name() string           { return "SystemVolume" }
func (p *SystemVolumePlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *SystemVolumePlugin) IncomingTypes() []string {
	return []string{"kdeconnect.systemvolume.request"}
}
func (p *SystemVolumePlugin) OutgoingTypes() []string { return []string{"kdeconnect.systemvolume"} }

func (p *SystemVolumePlugin) OnConnect(dev device.Peer) {
	if !p.audio.Available() {
		return
	}
	go func() {
		sinks := p.getSinks()
		pkt, err := protocol.NewPacket("kdeconnect.systemvolume", sinkListBody{SinkList: sinks})
		if err != nil {
			return
		}
		_ = dev.Send(pkt)
	}()
}

func (p *SystemVolumePlugin) OnDisconnect(dev device.Peer) {}
