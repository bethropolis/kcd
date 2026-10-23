package presenter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

func TestPresenterDeltasAccumulateAndStopResets(t *testing.T) {
	p := &PresenterPlugin{
		logger:  log.Nop(),
		xPos:    0.5,
		yPos:    0.5,
		ratio:   1.0,
		screenW: 1920,
		screenH: 1080,
		moveCh:  make(chan struct{}, 1),
	}

	dx1, dy1 := 0.1, 0.2
	body1, _ := json.Marshal(PresenterBody{Dx: &dx1, Dy: &dy1})
	if err := p.Handle(context.Background(), nil, &protocol.Packet{Body: body1}); err != nil {
		t.Fatalf("Handle 1: %v", err)
	}

	dx2, dy2 := 0.05, -0.1
	body2, _ := json.Marshal(PresenterBody{Dx: &dx2, Dy: &dy2})
	if err := p.Handle(context.Background(), nil, &protocol.Packet{Body: body2}); err != nil {
		t.Fatalf("Handle 2: %v", err)
	}

	p.mu.Lock()
	if p.xPos < 0.64 || p.xPos > 0.66 {
		t.Errorf("expected xPos ~0.65, got %f", p.xPos)
	}
	if p.yPos < 0.59 || p.yPos > 0.61 {
		t.Errorf("expected yPos ~0.60, got %f", p.yPos)
	}
	p.mu.Unlock()

	stop := true
	stopBody, _ := json.Marshal(PresenterBody{Stop: &stop})
	if err := p.Handle(context.Background(), nil, &protocol.Packet{Body: stopBody}); err != nil {
		t.Fatalf("Handle stop: %v", err)
	}

	p.mu.Lock()
	if p.xPos != 0.5 || p.yPos != 0.5 {
		t.Errorf("expected reset to 0.5, got x=%f, y=%f", p.xPos, p.yPos)
	}
	p.mu.Unlock()
}
