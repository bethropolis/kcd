package mpris

import (
	"context"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/log"
)

// storeLocalState caches the latest known state for a local player, then
// syncs the position poller. It is the single choke point for all
// lastStates writes, so every observed change — signal or poll — has a
// chance to (re)arm the poller.
func (p *MPRISPlugin) storeLocalState(displayName string, state *NowPlaying) {
	p.mu.Lock()
	p.lastStates[displayName] = state
	p.syncPlayingPollerLocked()
	p.mu.Unlock()
}

// syncPlayingPollerLocked reconciles the poller with what we know.
//
// It deliberately does NOT trust the cached IsPlaying flag to decide
// whether to run. The cache is only refreshed by a signal or by the
// poller itself, so gating on it deadlocks: lose one PlaybackStatus
// signal and the cache stays "paused", so the poller never arms, so
// nothing ever refreshes the cache — and no further PlaybackStatus
// signal arrives until the next pause/play. The removed 2s timer used
// to break that deadlock by refreshing unconditionally.
//
// Instead any observed change arms the poller, and the poller decides
// its own lifetime from live D-Bus reads. A paused desktop pays one
// extra tick per signal, then goes quiet.
//
// Callers must hold p.mu.
func (p *MPRISPlugin) syncPlayingPollerLocked() {
	if !p.mprisCfg.PollWhilePlaying {
		p.stopPlayingPollerLocked()
		return
	}
	if len(p.players) == 0 {
		p.stopPlayingPollerLocked()
		return
	}
	p.armPlayingPollerLocked()
	p.startWatchdogLocked()
}

// startWatchdogLocked starts the slow re-check if it is not already
// running. Callers must hold p.mu.
func (p *MPRISPlugin) startWatchdogLocked() {
	if p.watchdogCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(p.watchCtx)
	p.watchdogCancel = cancel
	go p.runWatchdog(ctx)
}

// stopWatchdogLocked stops the slow re-check. Callers must hold p.mu.
func (p *MPRISPlugin) stopWatchdogLocked() {
	if p.watchdogCancel != nil {
		p.watchdogCancel()
		p.watchdogCancel = nil
	}
}

// runWatchdog restarts the position poller when it finds playback the
// signal path failed to announce. It stays parked — one read per
// interval — whenever the poller is already doing that job itself.
func (p *MPRISPlugin) runWatchdog(ctx context.Context) {
	ticker := time.NewTicker(watchdogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.mu.RLock()
			armed := p.pollCancel != nil
			tracked := len(p.players)
			p.mu.RUnlock()

			if armed || tracked == 0 {
				continue
			}
			if !p.anyPlayerPlaying() {
				continue
			}
			p.logger.Debug("mpris: watchdog found unpolled playback, arming position poller")
			p.mu.Lock()
			p.armPlayingPollerLocked()
			p.mu.Unlock()
		}
	}
}

// anyPlayerPlaying reports whether any tracked player is playing right
// now. It reads live state without touching the cache or broadcasting —
// this only decides whether to restart the poller.
func (p *MPRISPlugin) anyPlayerPlaying() bool {
	p.mu.RLock()
	players := make([]*trackedPlayer, 0, len(p.players))
	for _, pl := range p.players {
		players = append(players, pl)
	}
	p.mu.RUnlock()

	for _, pl := range players {
		state, err := p.playerState(pl.displayName)
		if err == nil && state.IsPlaying {
			return true
		}
	}
	return false
}

// armPlayingPollerLocked starts the position ticker if it is not already
// running. Callers must hold p.mu.
func (p *MPRISPlugin) armPlayingPollerLocked() {
	if p.pollCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(p.watchCtx)
	p.pollCancel = cancel
	// A generation token lets a poller that stops itself avoid clearing
	// the handle of the poller that replaced it.
	p.pollGen++
	gen := p.pollGen
	interval := config.Duration(p.mprisCfg.PositionInterval)
	p.logger.Debug("mpris: arming position poller", log.Duration("interval", interval))
	go p.runPlayingPoller(ctx, interval, gen)
}

// stopPlayingPollerLocked cancels a running position poller, if any.
// Callers must hold p.mu. Cancelling is non-blocking; the goroutine exits
// on its own via ctx.Done.
func (p *MPRISPlugin) stopPlayingPollerLocked() {
	if p.pollCancel != nil {
		p.pollCancel()
		p.pollCancel = nil
	}
}

// maxConsecutiveReadFailures bounds how long the poller keeps retrying
// when every live read errors. A player that is merely slow to answer
// must not end sampling; one that is gone for good should not pin a
// ticker on. Its name disappearing handles that case, so this is only a
// backstop for a name that lingers with a dead object behind it.
const maxConsecutiveReadFailures = 5

// watchdogInterval is how often the slow re-check looks for playback that
// the signal path missed.
//
// The poller stops the moment a live read reports nothing playing, and it
// only restarts when an observed change re-arms it. That makes it hostage
// to signal delivery: lose the PlaybackStatus=Playing edge and the poller
// stays down for the rest of the session, so the phone's now-playing
// freezes even though audio is playing. Firefox's MPRIS endpoint answers
// intermittently, which makes that a routine event, not a corner case.
//
// So the watchdog samples once per interval and restarts the poller if it
// finds something playing. 10s bounds how stale the phone's now-playing
// can get after a resume, at six reads per minute while an MPRIS app sits
// paused — still far below the 30/min the poller itself costs while
// playing, and it disappears entirely once no player is tracked.
const watchdogInterval = 10 * time.Second

// runPlayingPoller re-reads D-Bus state for every tracked player at
// PositionInterval. It returns once live reads say nothing is playing, so
// its lifetime follows the player rather than the cache that armed it.
// Paused players and an empty player list cost zero wakeups — that is the
// entire point: idle desktops stay silent.
func (p *MPRISPlugin) runPlayingPoller(ctx context.Context, interval time.Duration, gen uint64) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer p.finishPlayingPoller(gen)

	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			playing, complete := p.pollPlayingPlayers()
			switch {
			case playing:
				failures = 0
			case complete:
				// Every player answered and none is playing: a real stop.
				p.logger.Debug("mpris: no player is playing, stopping position poller")
				return
			default:
				// A read failed. That is not evidence playback ended —
				// Firefox's MPRIS endpoint answers intermittently — so keep
				// sampling, but do not do it forever.
				failures++
				if failures >= maxConsecutiveReadFailures {
					p.logger.Warn("mpris: position poller gave up after repeated read failures",
						log.Int("failures", failures))
					return
				}
			}
		}
	}
}

// finishPlayingPoller releases the poller slot when this poller stops on
// its own. It only clears the handle if a later arm has not already
// replaced it, so a self-stopping poller cannot orphan its successor.
func (p *MPRISPlugin) finishPlayingPoller(gen uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pollGen == gen {
		p.pollCancel = nil
	}
}

// pollPlayingPlayers samples every tracked player once, broadcasting the
// ones whose state actually changed.
//
// It reports whether anything is playing, and whether every tracked
// player answered. An unanswered player makes the result incomplete: the
// caller must not read that as "playback ended".
func (p *MPRISPlugin) pollPlayingPlayers() (playing, complete bool) {
	p.mu.RLock()
	players := make([]*trackedPlayer, 0, len(p.players))
	for _, pl := range p.players {
		players = append(players, pl)
	}
	p.mu.RUnlock()

	if len(players) == 0 {
		return false, true
	}

	complete = true
	for _, pl := range players {
		state, err := p.playerState(pl.displayName)
		if err != nil {
			complete = false
			continue
		}
		if state.IsPlaying {
			playing = true
		}
		// Hold RLock through the compare: it reads the cached anchor,
		// which broadcast stamps under p.mu.
		p.mu.RLock()
		changed := localStateChanged(time.Now().UnixMilli(), state, p.lastStates[pl.displayName])
		p.mu.RUnlock()

		if changed {
			p.storeLocalState(pl.displayName, state)
			p.broadcast(state)
		}
	}
	return playing, complete
}
