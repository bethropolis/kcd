package mousepad

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
)

func (p *MousepadPlugin) Handle(_ context.Context, _ device.Peer, pkt *protocol.Packet) error {
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

const minMoveInterval = 8 * time.Millisecond

// worker is the single persistent goroutine that processes all mousepad events.
func (p *MousepadPlugin) worker() {
	var (
		moveTimer     *time.Timer
		moveTimerCh   <-chan time.Time
		lastMoveFlush time.Time
	)
	defer func() {
		if moveTimer != nil {
			moveTimer.Stop()
		}
	}()

	flushMove := func() {
		p.queueMu.Lock()
		stepX := int32(p.moveX)
		stepY := int32(p.moveY)
		var (
			moveBody MousepadBody
			hasMove  bool
		)
		if stepX != 0 || stepY != 0 {
			hasMove = true
			moveBody = MousepadBody{
				Dx: float64(stepX),
				Dy: float64(stepY),
			}
			p.moveX -= float64(stepX)
			p.moveY -= float64(stepY)
		}
		p.queueMu.Unlock()

		if hasMove {
			lastMoveFlush = time.Now()
			p.handleMove(moveBody)
		}
	}

	for {
		select {
		case <-p.ctx.Done():
			return

		case <-moveTimerCh:
			moveTimerCh = nil
			flushMove()

		case <-p.queueWake:
			for {
				p.queueMu.Lock()
				hasEvent := len(p.queue) > 0
				var eventBody MousepadBody
				if hasEvent {
					eventBody = p.queue[0]
					p.queue = p.queue[1:]
				}
				stepX := int32(p.moveX)
				stepY := int32(p.moveY)
				hasPendingMove := stepX != 0 || stepY != 0
				p.queueMu.Unlock()

				// If there's an event (click, key, etc.), flush pending move first to preserve ordering
				if hasEvent {
					if moveTimer != nil {
						moveTimer.Stop()
						moveTimerCh = nil
					}
					if hasPendingMove {
						flushMove()
					}
					if eventBody.Scroll {
						p.handleMove(eventBody)
					} else {
						p.handleEvent(eventBody)
					}
					continue
				}

				if !hasPendingMove {
					break
				}

				// Check rate limit: flush at most every ~8ms
				sinceLast := time.Since(lastMoveFlush)
				if sinceLast >= minMoveInterval {
					if moveTimer != nil {
						moveTimer.Stop()
						moveTimerCh = nil
					}
					flushMove()
				} else {
					if moveTimer == nil {
						moveTimer = time.NewTimer(minMoveInterval - sinceLast)
						moveTimerCh = moveTimer.C
					}
					break
				}
			}
		}
	}
}

// Close stops the background worker goroutine and releases resources.
func (p *MousepadPlugin) Close() error {
	p.cancel()
	p.xdotoolMu.Lock()
	if p.xdotoolIn != nil {
		_ = p.xdotoolIn.Close()
		p.xdotoolIn = nil
	}
	p.xdotoolMu.Unlock()
	p.ydotoolMu.Lock()
	if p.ydotoolConn != nil {
		_ = p.ydotoolConn.Close()
		p.ydotoolConn = nil
	}
	p.ydotoolMu.Unlock()
	return nil
}
