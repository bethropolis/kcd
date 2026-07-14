package remotesystemvolume

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

type VolumeBody struct {
	RequestSinks bool       `json:"requestSinks,omitempty"`
	Name         string     `json:"name,omitempty"`
	Volume       int        `json:"volume,omitempty"`
	Muted        bool       `json:"muted,omitempty"`
	MaxVolume    int        `json:"maxVolume,omitempty"`
	SinkList     []SinkInfo `json:"sinkList,omitempty"`
}

type SinkInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Volume      int    `json:"volume"`
	Muted       bool   `json:"muted"`
	MaxVolume   int    `json:"maxVolume"`
}

type RemoteSystemVolumePlugin struct {
	logger *zap.Logger
	bus    *events.Bus
	sinks  sync.Map // device ID -> []SinkInfo
}

func NewRemoteSystemVolumePlugin(bus *events.Bus, logger *zap.Logger) *RemoteSystemVolumePlugin {
	return &RemoteSystemVolumePlugin{
		logger: logger.With(zap.String("plugin", "remotesystemvolume")),
		bus:    bus,
	}
}

func (p *RemoteSystemVolumePlugin) Name() string           { return "RemoteSystemVolume" }
func (p *RemoteSystemVolumePlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *RemoteSystemVolumePlugin) IncomingTypes() []string {
	return []string{"kdeconnect.systemvolume"}
}
func (p *RemoteSystemVolumePlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.systemvolume.request"}
}

func (p *RemoteSystemVolumePlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body VolumeBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	if body.SinkList != nil {
		p.sinks.Store(dev.ID(), body.SinkList)
		if p.bus != nil {
			p.bus.Publish(events.TypeVolumeUpdate, dev.ID(), map[string]any{
				"sinks": body.SinkList,
			})
		}
		return nil
	}

	if body.Name != "" {
		if val, ok := p.sinks.Load(dev.ID()); ok {
			sinks := val.([]SinkInfo)
			for i, s := range sinks {
				if s.Name == body.Name {
					sinks[i].Volume = body.Volume
					sinks[i].Muted = body.Muted
					break
				}
			}
			p.sinks.Store(dev.ID(), sinks)
		}
		if p.bus != nil {
			p.bus.Publish(events.TypeVolumeUpdate, dev.ID(), map[string]any{
				"name":   body.Name,
				"volume": body.Volume,
				"muted":  body.Muted,
			})
		}
	}

	return nil
}

func (p *RemoteSystemVolumePlugin) ListSinks(deviceID string) []SinkInfo {
	if val, ok := p.sinks.Load(deviceID); ok {
		return val.([]SinkInfo)
	}
	return nil
}

func (p *RemoteSystemVolumePlugin) SetVolume(dev device.Sender, sinkName string, volume int) error {
	pkt, err := protocol.NewPacket("kdeconnect.systemvolume.request", VolumeBody{
		Name:   sinkName,
		Volume: volume,
		Muted:  false,
	})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

func (p *RemoteSystemVolumePlugin) SetMuted(dev device.Sender, sinkName string, muted bool) error {
	pkt, err := protocol.NewPacket("kdeconnect.systemvolume.request", VolumeBody{
		Name:  sinkName,
		Muted: muted,
	})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

func (p *RemoteSystemVolumePlugin) RequestSinkList(dev device.Sender) error {
	pkt, err := protocol.NewPacket("kdeconnect.systemvolume.request", VolumeBody{
		RequestSinks: true,
	})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

func (p *RemoteSystemVolumePlugin) OnConnect(dev device.Sender) {
	go func() {
		if err := p.RequestSinkList(dev); err != nil {
			p.logger.Warn("failed to request sink list",
				zap.String("device", dev.ID()),
				zap.Error(err),
			)
		}
	}()
}
func (p *RemoteSystemVolumePlugin) OnDisconnect(dev device.Sender) {
	p.sinks.Delete(dev.ID())
}
