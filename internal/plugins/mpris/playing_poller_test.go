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
// reconcile state refresh: nil cache always counts, equal states don't,
// and position counts only on drift past the tolerance.
func TestLocalStateChanged(t *testing.T) {
	now := time.Now().UnixMilli()
	base := &NowPlaying{Player: "Nightdrive", PlaybackStatus: "Playing", IsPlaying: true, Title: "T"}
	if !localStateChanged(now, base, nil) {
		t.Fatal("nil cache must count as changed")
	}
	same := &NowPlaying{Player: "Nightdrive", PlaybackStatus: "Playing", IsPlaying: true, Title: "T"}
	if localStateChanged(now, base, same) {
		t.Fatal("equal states must not count as changed")
	}
	paused := &NowPlaying{Player: "Nightdrive", PlaybackStatus: "Paused", IsPlaying: false, Title: "T"}
	if !localStateChanged(now, base, paused) {
		t.Fatal("status flip must count as changed")
	}
}

// Steady playback inside the tolerance must stay silent: the phone
// extrapolates from the anchor, so an on-schedule tick is not a change.
func TestLocalStateChangedSteadyPlaybackSilent(t *testing.T) {
	now := time.Now().UnixMilli()
	last := &NowPlaying{Player: "Nightdrive", PlaybackStatus: "Playing", IsPlaying: true, Title: "T", Pos: 40000, PosAnchorMs: now - 2000}
	onSchedule := &NowPlaying{Player: "Nightdrive", PlaybackStatus: "Playing", IsPlaying: true, Title: "T", Pos: 42000}
	if localStateChanged(now, onSchedule, last) {
		t.Fatal("on-schedule position must not count as changed")
	}
}

// A seek jumps off the extrapolation: the tick must re-broadcast and
// refresh the phone's anchor.
func TestLocalStateChangedSeekCounts(t *testing.T) {
	now := time.Now().UnixMilli()
	last := &NowPlaying{Player: "Nightdrive", PlaybackStatus: "Playing", IsPlaying: true, Title: "T", Pos: 40000, PosAnchorMs: now - 2000}
	seeked := &NowPlaying{Player: "Nightdrive", PlaybackStatus: "Playing", IsPlaying: true, Title: "T", Pos: 90000}
	if !localStateChanged(now, seeked, last) {
		t.Fatal("seek off the extrapolation must count as changed")
	}
}

// A stall (position not advancing though playing) drifts off the
// extrapolation the other way and must also refresh.
func TestLocalStateChangedStallCounts(t *testing.T) {
	now := time.Now().UnixMilli()
	last := &NowPlaying{Player: "Nightdrive", PlaybackStatus: "Playing", IsPlaying: true, Title: "T", Pos: 40000, PosAnchorMs: now - 10000}
	stalled := &NowPlaying{Player: "Nightdrive", PlaybackStatus: "Playing", IsPlaying: true, Title: "T", Pos: 42000}
	if !localStateChanged(now, stalled, last) {
		t.Fatal("stalled position must count as changed")
	}
}

// Paused players don't extrapolate: any raw Pos difference counts, and
// equal positions stay silent.
func TestLocalStateChangedPausedPosition(t *testing.T) {
	now := time.Now().UnixMilli()
	last := &NowPlaying{Player: "Nightdrive", PlaybackStatus: "Paused", IsPlaying: false, Title: "T", Pos: 10000, PosAnchorMs: now - 60000}
	seeked := &NowPlaying{Player: "Nightdrive", PlaybackStatus: "Paused", IsPlaying: false, Title: "T", Pos: 30000}
	if !localStateChanged(now, seeked, last) {
		t.Fatal("paused seek must count as changed")
	}
	same := &NowPlaying{Player: "Nightdrive", PlaybackStatus: "Paused", IsPlaying: false, Title: "T", Pos: 10000}
	if localStateChanged(now, same, last) {
		t.Fatal("unchanged paused position must not count as changed")
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
