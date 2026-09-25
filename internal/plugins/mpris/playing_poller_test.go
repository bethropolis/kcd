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

func pollerArmed(p *MPRISPlugin) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pollCancel != nil
}

func trackPlayer(p *MPRISPlugin, name string) {
	p.mu.Lock()
	p.players[name] = &trackedPlayer{displayName: name}
	p.mu.Unlock()
}

// An observed state change must arm the poller even when the cached
// IsPlaying flag says paused. This is the regression guard: gating the
// arm on the cache deadlocks, because a single lost PlaybackStatus
// signal leaves the cache stale forever and the poller — the only other
// thing that refreshes it — disarmed.
func TestPollArmsOnObservedChange(t *testing.T) {
	p := NewMPRISPlugin(nil, events.NewBus(log.Nop()), false, testMPRISConfig(), log.Nop())
	defer p.watchCancel()

	trackPlayer(p, "Nightdrive")
	if pollerArmed(p) {
		t.Fatal("poller armed before any state was observed")
	}

	// Cached state says paused, but something changed, so the poller must
	// start and verify against live D-Bus state itself.
	p.storeLocalState("Nightdrive", &NowPlaying{Player: "Nightdrive", IsPlaying: false})
	if !pollerArmed(p) {
		t.Fatal("poller not armed after an observed change (deadlock regression)")
	}
}

// With no tracked players there is nothing to poll, so no poller.
func TestPollNotArmedWithoutPlayers(t *testing.T) {
	p := NewMPRISPlugin(nil, events.NewBus(log.Nop()), false, testMPRISConfig(), log.Nop())
	defer p.watchCancel()

	p.storeLocalState("Ghost FM", &NowPlaying{Player: "Ghost FM", IsPlaying: true})
	if pollerArmed(p) {
		t.Fatal("poller armed with zero tracked players")
	}
}

// A live read that FAILS is not evidence that playback ended — Firefox's
// MPRIS endpoint answers intermittently, and treating an error as "not
// playing" used to strand the poller on the first hiccup. The poller must
// retry through failures and only give up after the bounded backstop.
func TestPollSurvivesReadFailures(t *testing.T) {
	cfg := testMPRISConfig()
	cfg.PositionInterval = "10ms"
	p := NewMPRISPlugin(nil, events.NewBus(log.Nop()), false, cfg, log.Nop())
	defer p.watchCancel()

	// With no usable D-Bus connection every read fails, so this exercises
	// the failure path exactly.
	trackPlayer(p, "Flaky FM")
	p.storeLocalState("Flaky FM", &NowPlaying{Player: "Flaky FM", IsPlaying: true})
	if !pollerArmed(p) {
		t.Fatal("poller not armed after an observed change")
	}

	// It must still be running well past the first failure.
	time.Sleep(maxConsecutiveReadFailures * 3 * time.Millisecond)
	if !pollerArmed(p) {
		t.Fatal("poller stopped on a read failure instead of retrying")
	}

	// The bounded backstop must still release the slot rather than run
	// forever on a name with a dead object behind it.
	deadline := time.Now().Add(5 * time.Second)
	for pollerArmed(p) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if pollerArmed(p) {
		t.Fatal("poller never gave up after the bounded failure backstop")
	}
}

// A self-stopping poller must not clear the handle of the poller that
// replaced it, or the successor becomes unstoppable and a second ticker
// keeps running.
func TestSelfStoppingPollerDoesNotOrphanSuccessor(t *testing.T) {
	cfg := testMPRISConfig()
	cfg.PositionInterval = "10ms"
	p := NewMPRISPlugin(nil, events.NewBus(log.Nop()), false, cfg, log.Nop())
	defer p.watchCancel()

	trackPlayer(p, "Paused FM")
	p.storeLocalState("Paused FM", &NowPlaying{Player: "Paused FM", IsPlaying: false})

	// Let the first poller arm, then simulate it self-stopping after a
	// successor has already taken the slot.
	p.mu.Lock()
	firstGen := p.pollGen
	p.pollGen++
	successorGen := p.pollGen
	p.mu.Unlock()

	p.finishPlayingPoller(firstGen)

	p.mu.RLock()
	cleared := p.pollCancel == nil
	p.mu.RUnlock()
	if cleared {
		t.Fatal("a stale poller cleared its successor's handle")
	}

	// The current generation may clear the slot.
	p.finishPlayingPoller(successorGen)
	if pollerArmed(p) {
		t.Fatal("current poller generation failed to release the slot")
	}
}

// Removing the last player must stop the poller even though no state
// update flows through storeLocalState.
func TestPollDisarmsOnPlayerRemoval(t *testing.T) {
	p := NewMPRISPlugin(nil, events.NewBus(log.Nop()), false, testMPRISConfig(), log.Nop())
	defer p.watchCancel()

	trackPlayer(p, "Nightdrive")
	p.storeLocalState("Nightdrive", &NowPlaying{Player: "Nightdrive", IsPlaying: true})
	if !pollerArmed(p) {
		t.Fatal("poller not armed before removal")
	}

	p.removePlayer("Nightdrive")

	if pollerArmed(p) {
		t.Fatal("poller still armed after last player removed")
	}
}

// The watchdog exists because signal delivery is unreliable: the poller
// stops on a confirmed pause and must be able to come back on its own
// when playback resumes. It starts with the first tracked player and is
// torn down with the last, so a desktop with no MPRIS app keeps no
// timers at all.
func TestWatchdogLifecycleFollowsTrackedPlayers(t *testing.T) {
	p := NewMPRISPlugin(nil, events.NewBus(log.Nop()), false, testMPRISConfig(), log.Nop())
	defer p.watchCancel()

	watchdogRunning := func() bool {
		p.mu.RLock()
		defer p.mu.RUnlock()
		return p.watchdogCancel != nil
	}

	if watchdogRunning() {
		t.Fatal("watchdog running with zero tracked players")
	}

	trackPlayer(p, "Nightdrive")
	p.storeLocalState("Nightdrive", &NowPlaying{Player: "Nightdrive", IsPlaying: false})
	if !watchdogRunning() {
		t.Fatal("watchdog not started alongside the first tracked player")
	}

	trackPlayer(p, "Second FM")
	p.storeLocalState("Second FM", &NowPlaying{Player: "Second FM", IsPlaying: false})
	if !watchdogRunning() {
		t.Fatal("watchdog stopped while players remain")
	}

	p.removePlayer("Nightdrive")
	if !watchdogRunning() {
		t.Fatal("watchdog stopped while one player remains")
	}

	p.removePlayer("Second FM")
	if watchdogRunning() {
		t.Fatal("watchdog still running after the last player was removed")
	}
}

// With PollWhilePlaying=false the poller must never arm (pure
// event-driven mode).
func TestPollNeverArmsWhenDisabled(t *testing.T) {
	p := NewMPRISPlugin(nil, events.NewBus(log.Nop()), false, config.MPRISConfig{}, log.Nop())
	defer p.watchCancel()

	trackPlayer(p, "Nightdrive")
	p.storeLocalState("Nightdrive", &NowPlaying{Player: "Nightdrive", IsPlaying: true})

	if pollerArmed(p) {
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
	defer p.watchCancel()

	state := &NowPlaying{Player: "Nightdrive", Pos: 42000, IsPlaying: true}
	before := time.Now().UnixMilli()
	p.broadcast(state)
	after := time.Now().UnixMilli()

	if state.PosAnchorMs < before || state.PosAnchorMs > after {
		t.Fatalf("PosAnchorMs %d not stamped at broadcast time [%d, %d]",
			state.PosAnchorMs, before, after)
	}
}
