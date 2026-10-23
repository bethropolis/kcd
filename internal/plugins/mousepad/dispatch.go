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

	isPointerMove := (body.Dx != 0 || body.Dy != 0) && !body.Scroll && !body.SingleClick && !body.DoubleClick &&
		!body.RightClick && !body.MiddleClick && !body.SingleHold && !body.SingleRel &&
		body.Key == "" && body.SpecialKey == 0

	p.queueMu.Lock()
	if isPointerMove {
		p.moveX += body.Dx
		p.moveY += body.Dy
	} else {
		p.queue = append(p.queue, body)
	}
	p.queueMu.Unlock()

	select {
	case p.queueWake <- struct{}{}:
	default:
	}
	return nil
}

// worker is the single persistent goroutine that processes all mousepad events.
func (p *MousepadPlugin) worker() {
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.queueWake:
			for {
				p.queueMu.Lock()
				var (
					hasMove   bool
					moveBody  MousepadBody
					hasEvent  bool
					eventBody MousepadBody
				)
				stepX := int32(p.moveX)
				stepY := int32(p.moveY)
				if stepX != 0 || stepY != 0 {
					hasMove = true
					moveBody = MousepadBody{
						Dx: float64(stepX),
						Dy: float64(stepY),
					}
					p.moveX -= float64(stepX)
					p.moveY -= float64(stepY)
				}
				if len(p.queue) > 0 {
					hasEvent = true
					eventBody = p.queue[0]
					p.queue = p.queue[1:]
				}
				p.queueMu.Unlock()

				if !hasMove && !hasEvent {
					break
				}
				if hasMove {
					p.handleMove(moveBody)
				}
				if hasEvent {
					if eventBody.Scroll {
						p.handleMove(eventBody)
					} else {
						p.handleEvent(eventBody)
					}
				}
			}
		}
	}
}

// Close stops the background worker goroutine.
func (p *MousepadPlugin) Close() error {
	p.cancel()
	return nil
}
