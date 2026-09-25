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

// runPlayingPoller re-reads D-Bus state for every tracked player at
// PositionInterval. It returns once a live read says nothing is playing,
// so its lifetime follows the player rather than the cache that armed
// it. Paused players and an empty player list cost zero wakeups — that is
// the entire point: idle desktops stay silent.
func (p *MPRISPlugin) runPlayingPoller(ctx context.Context, interval time.Duration, gen uint64) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer p.finishPlayingPoller(gen)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !p.pollPlayingPlayers() {
				p.logger.Debug("mpris: no player is playing, stopping position poller")
				return
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
// ones whose state actually changed. It reports whether anything is
// playing according to those live reads — the sole input to the
// poller's stop decision.
func (p *MPRISPlugin) pollPlayingPlayers() bool {
	p.mu.RLock()
	players := make([]*trackedPlayer, 0, len(p.players))
	for _, pl := range p.players {
		players = append(players, pl)
	}
	p.mu.RUnlock()

	playing := false
	for _, pl := range players {
		state, err := p.playerState(pl.displayName)
		if err != nil {
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
	return playing
}
