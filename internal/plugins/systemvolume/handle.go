package systemvolume

import (
	"context"
	"encoding/json"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

func (p *SystemVolumePlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	if !p.audio.Available() {
		return nil // No audio backend, silently ignore.
	}

	var body VolumeBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	// Phone is requesting the list of audio sinks.
	if body.RequestSinks {
		go p.sendSinkList(dev)
		return nil
	}

	if body.Volume == nil && body.Muted == nil {
		if body.Enabled != nil && *body.Enabled {
			go p.sendSinkList(dev)
		}
		return nil
	}

	go func() {
		if body.Name == "" {
			body.Name = "@DEFAULT_AUDIO_SINK@"
		}
		var err error
		switch {
		case body.Volume != nil && body.Muted != nil:
			err = p.setVolume(body.Name, *body.Volume, *body.Muted)
		case body.Volume != nil:
			err = p.setVolumeOnly(*body.Volume)
		case body.Muted != nil:
			err = p.setMutedOnly(*body.Muted)
		}
		if err != nil {
			p.logger.Warn("systemvolume: failed to set volume", log.Error(err))
			return
		}
		sinks := p.getSinks()
		if p.bus != nil && len(sinks) > 0 {
			p.bus.Publish(events.TypeVolumeUpdate, dev.ID(), map[string]any{"name": sinks[0].Name, "volume": sinks[0].Volume, "muted": sinks[0].Muted})
		}
		p.sendSinks(dev, sinks)
	}()

	return nil
}

func (p *SystemVolumePlugin) sendSinkList(dev device.Sender) {
	p.sendSinks(dev, p.getSinks())
}

func (p *SystemVolumePlugin) sendSinks(dev device.Sender, sinks []SinkInfo) {
	pkt, err := protocol.NewPacket("kdeconnect.systemvolume", sinkListBody{SinkList: sinks})
	if err != nil {
		p.logger.Error("systemvolume: failed to create sink list packet", log.Error(err))
		return
	}
	if err := dev.Send(pkt); err != nil {
		p.logger.Error("systemvolume: failed to send sink list", log.Error(err))
	}
}

func (p *SystemVolumePlugin) OnConnect(dev device.Sender) {
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
func (p *SystemVolumePlugin) OnDisconnect(dev device.Sender) {}
