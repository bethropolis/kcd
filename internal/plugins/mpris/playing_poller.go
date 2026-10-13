package mpris

import (
	"context"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/log"
)

// storeLocalState is the single choke point for lastStates writes, so
// every observed change gets a chance to (re)arm the poller.
func (p *MPRISPlugin) storeLocalState(displayName string, state *NowPlaying) {
	p.mu.Lock()
	p.lastStates[displayName] = state
	p.syncPlayingPollerLocked()
	p.mu.Unlock()
}

// syncPlayingPollerLocked arms the poller on any change rather than the
// cached flag, where one lost signal deadlocks it paused. Callers hold p.mu.
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

// stopPlayingPollerLocked cancels a running poller, if any. Callers hold p.mu;
// cancelling is non-blocking, the goroutine exits on ctx.Done itself.
func (p *MPRISPlugin) stopPlayingPollerLocked() {
	if p.pollCancel != nil {
		p.pollCancel()
		p.pollCancel = nil
	}
}

// maxConsecutiveReadFailures bounds retries when every live read errors: a
// backstop for names lingering with dead objects behind them, not slow players.
const maxConsecutiveReadFailures = 5

// runPlayingPoller re-reads D-Bus state at PositionInterval, returning once
// nothing is playing: idle desktops hold zero wakeups by design.
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
				// A failed read is not evidence playback ended (Firefox answers
				// intermittently): keep sampling, but not forever.
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

// finishPlayingPoller releases the slot on self-stop, clearing the handle
// only if no later arm replaced it, so successors are never orphaned.
func (p *MPRISPlugin) finishPlayingPoller(gen uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pollGen == gen {
		p.pollCancel = nil
	}
}

// pollPlayingPlayers samples tracked players, broadcasting real changes. An
// unanswered player makes the result incomplete, not "playback ended".
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
