package digitizer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

const (
	defaultWidth  = 1920
	defaultHeight = 1080
)

func (p *DigitizerPlugin) Handle(_ context.Context, dev device.Peer, pkt *protocol.Packet) error {
	switch pkt.Type {
	case "kdeconnect.digitizer.session":
		var body SessionBody
		if err := json.Unmarshal(pkt.Body, &body); err != nil {
			return fmt.Errorf("digitizer: decode session body: %w", err)
		}
		p.handleSession(dev, body)

	case "kdeconnect.digitizer":
		p.mu.Lock()
		session := p.sessions[dev.ID()]
		p.mu.Unlock()

		if session == nil {
			// Spec: packets must not be processed without an active session
			return nil
		}

		var body DigitizerBody
		if err := json.Unmarshal(pkt.Body, &body); err != nil {
			return fmt.Errorf("digitizer: decode event body: %w", err)
		}

		if err := session.SendEvent(body); err != nil {
			p.logger.Warn("digitizer: send event failed", log.String("device_id", dev.ID()), log.Error(err))
		}
	}
	return nil
}

func (p *DigitizerPlugin) handleSession(dev device.Peer, body SessionBody) {
	p.mu.Lock()
	existing := p.sessions[dev.ID()]
	if existing != nil {
		delete(p.sessions, dev.ID())
	}
	p.mu.Unlock()

	if existing != nil {
		_ = existing.Close()
	}

	switch body.Action {
	case "start":
		width := body.Width
		height := body.Height
		if width <= 0 {
			width = defaultWidth
		}
		if height <= 0 {
			height = defaultHeight
		}

		tab, err := p.creator(width, height)
		if err != nil {
			p.logger.Warn("digitizer: failed to create tablet device",
				log.String("device_id", dev.ID()),
				log.Error(err),
			)
			return
		}

		p.mu.Lock()
		p.sessions[dev.ID()] = tab
		p.mu.Unlock()

		p.logger.Info("digitizer: session started",
			log.String("device_id", dev.ID()),
			log.Int("width", int(width)),
			log.Int("height", int(height)),
		)

		if p.bus != nil {
			p.bus.Publish(events.TypeDigitizerSession, dev.ID(), map[string]any{
				"active": true,
				"width":  width,
				"height": height,
			})
		}

	case "end":
		p.logger.Info("digitizer: session ended by peer", log.String("device_id", dev.ID()))
		if p.bus != nil {
			p.bus.Publish(events.TypeDigitizerSession, dev.ID(), map[string]any{
				"active": false,
			})
		}
	}
}
