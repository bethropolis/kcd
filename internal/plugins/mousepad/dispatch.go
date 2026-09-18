package mousepad

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
)

func (p *MousepadPlugin) Handle(_ context.Context, _ device.Sender, pkt *protocol.Packet) error {
	var body MousepadBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return fmt.Errorf("mousepad: decode body: %w", err)
	}

	isPointerMove := (body.Dx != 0 || body.Dy != 0) && !body.SingleClick && !body.DoubleClick &&
		!body.RightClick && !body.MiddleClick && !body.SingleHold && !body.SingleRel &&
		body.Key == "" && body.SpecialKey == 0

	if isPointerMove {
		// Drop stale frame if worker hasn't consumed the last one yet.
		select {
		case p.moveCh <- body:
		default:
			select {
			case <-p.moveCh: // drain
			default:
			}
			p.moveCh <- body
		}
	} else {
		p.eventCh <- body
	}
	return nil
}

// worker is the single persistent goroutine that processes all mousepad events.
func (p *MousepadPlugin) worker() {
	for {
		select {
		case <-p.ctx.Done():
			return
		case body, ok := <-p.moveCh:
			if !ok {
				return
			}
			p.handleMove(body)
		case body, ok := <-p.eventCh:
			if !ok {
				return
			}
			p.handleEvent(body)
		}
	}
}

// Close stops the background worker goroutine.
func (p *MousepadPlugin) Close() error {
	p.cancel()
	return nil
}
