package mpris

import (
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
)

func testMPRISConfig() config.MPRISConfig {
	return config.MPRISConfig{PollWhilePlaying: true, PositionInterval: "1h"}
}

// The poller must exist exactly while at least one tracked player reports
// IsPlaying — and never otherwise.
func TestPollArmsOnlyWhilePlaying(t *testing.T) {
	p := NewMPRISPlugin(nil, events.NewBus(log.Nop()), false, testMPRISConfig(), log.Nop())

	p.mu.RLock()
	armed := p.pollCancel != nil
	p.mu.RUnlock()
	if armed {
		t.Fatal("poller armed with zero players")
	}

	p.storeLocalState("Paused FM", &NowPlaying{Player: "Paused FM", IsPlaying: false})

	p.mu.RLock()
	armed = p.pollCancel != nil
	p.mu.RUnlock()
	if armed {
		t.Fatal("poller armed while only paused players tracked")
	}

	p.storeLocalState("Nightdrive", &NowPlaying{Player: "Nightdrive", IsPlaying: true})

	p.mu.RLock()
	armed = p.pollCancel != nil
	p.mu.RUnlock()
	if !armed {
		t.Fatal("poller not armed while a player IsPlaying")
	}

	// Pausing the last playing player must disarm (the 1h ticker never
	// fires, so no D-Bus traffic can occur during this test).
	p.storeLocalState("Nightdrive", &NowPlaying{Player: "Nightdrive", IsPlaying: false})

	p.mu.RLock()
	armed = p.pollCancel != nil
	p.mu.RUnlock()
	if armed {
		t.Fatal("poller still armed after last player paused")
	}
}

// Removing the last playing player must disarm even though no state
// update flows through storeLocalState.
func TestPollDisarmsOnPlayerRemoval(t *testing.T) {
	p := NewMPRISPlugin(nil, events.NewBus(log.Nop()), false, testMPRISConfig(), log.Nop())

	p.mu.Lock()
	p.players["Nightdrive"] = &trackedPlayer{displayName: "Nightdrive"}
	p.mu.Unlock()
	p.storeLocalState("Nightdrive", &NowPlaying{Player: "Nightdrive", IsPlaying: true})

	p.removePlayer("Nightdrive")

	p.mu.RLock()
	armed := p.pollCancel != nil
	p.mu.RUnlock()
	if armed {
		t.Fatal("poller still armed after playing player removed")
	}
}

// With PollWhilePlaying=false the poller must never arm (pure
// event-driven mode).
func TestPollNeverArmsWhenDisabled(t *testing.T) {
	p := NewMPRISPlugin(nil, events.NewBus(log.Nop()), false, config.MPRISConfig{}, log.Nop())

	p.storeLocalState("Nightdrive", &NowPlaying{Player: "Nightdrive", IsPlaying: true})

	p.mu.RLock()
	armed := p.pollCancel != nil
	p.mu.RUnlock()
	if armed {
		t.Fatal("poller armed with PollWhilePlaying=false")
	}
}

// localStateChanged is the shared compare behind the poller tick and the
// reconcile state refresh: nil cache always counts, equal states don't.
func TestLocalStateChanged(t *testing.T) {
	base := &NowPlaying{Player: "Nightdrive", PlaybackStatus: "Playing", IsPlaying: true, Title: "T"}
	if !localStateChanged(base, nil) {
		t.Fatal("nil cache must count as changed")
	}
	same := &NowPlaying{Player: "Nightdrive", PlaybackStatus: "Playing", IsPlaying: true, Title: "T"}
	if localStateChanged(base, same) {
		t.Fatal("equal states must not count as changed")
	}
	paused := &NowPlaying{Player: "Nightdrive", PlaybackStatus: "Paused", IsPlaying: false, Title: "T"}
	if !localStateChanged(base, paused) {
		t.Fatal("status flip must count as changed")
	}
}

// Anchor is stamped on the broadcast state itself.
func TestBroadcastAnchorFreshness(t *testing.T) {
	p := NewMPRISPlugin(nil, events.NewBus(log.Nop()), false, config.MPRISConfig{}, log.Nop())

	state := &NowPlaying{Player: "Nightdrive", Pos: 42000, IsPlaying: true}
	before := time.Now().UnixMilli()
	p.broadcast(state)
	after := time.Now().UnixMilli()

	if state.PosAnchorMs < before || state.PosAnchorMs > after {
		t.Fatalf("PosAnchorMs %d not stamped at broadcast time [%d, %d]",
			state.PosAnchorMs, before, after)
	}
}
