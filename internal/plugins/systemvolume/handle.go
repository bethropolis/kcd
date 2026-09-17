package systemvolume

import (
	"context"
	"encoding/json"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

func (p *SystemVolumePlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	if p.backend == "" {
		return nil // No audio backend, silently ignore.
	}

	var body VolumeBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	// Phone is requesting the list of audio sinks.
	if body.RequestSinks {
		go func() {
			sinks := p.getSinks()
			pkt, err := protocol.NewPacket("kdeconnect.systemvolume", sinkListBody{SinkList: sinks})
			if err != nil {
				p.logger.Error("systemvolume: failed to create sink list packet", zap.Error(err))
				return
			}
			if err := dev.Send(pkt); err != nil {
				p.logger.Error("systemvolume: failed to send sink list", zap.Error(err))
			}
		}()
		return nil
	}

	// Phone is setting volume or mute.
	go func() {
		if body.Name == "" {
			body.Name = "@DEFAULT_AUDIO_SINK@"
		}
		if err := p.setVolume(body.Name, body.Volume, body.Muted); err != nil {
			p.logger.Warn("systemvolume: failed to set volume", zap.Error(err))
			return
		}
		if p.bus != nil {
			p.bus.Publish(events.TypeVolumeUpdate, dev.ID(), map[string]any{
				"name":   body.Name,
				"volume": body.Volume,
				"muted":  body.Muted,
			})
		}
	}()

	return nil
}

func (p *SystemVolumePlugin) OnConnect(dev device.Sender) {
	if p.backend == "" {
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
func (p *SystemVolumePlugin) OnDisconnect(dev device.Sender) {}
