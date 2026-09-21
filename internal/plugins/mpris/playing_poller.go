package mpris

import (
	"context"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/log"
)

// storeLocalState caches the latest known state for a local player, then
// arms or disarms the position poller. It is the single choke point for
// all lastStates writes so the poller can never drift out of sync with
// what is actually playing.
func (p *MPRISPlugin) storeLocalState(displayName string, state *NowPlaying) {
	p.mu.Lock()
	p.lastStates[displayName] = state
	p.syncPlayingPollerLocked()
	p.mu.Unlock()
}

// syncPlayingPollerLocked starts the position ticker when at least one
// tracked player IsPlaying and stops it otherwise. With
// PollWhilePlaying=false the poller never runs (pure event-driven).
// Callers must hold p.mu.
func (p *MPRISPlugin) syncPlayingPollerLocked() {
	if !p.mprisCfg.PollWhilePlaying {
		p.stopPlayingPollerLocked()
		return
	}
	playing := false
	for _, s := range p.lastStates {
		if s != nil && s.IsPlaying {
			playing = true
			break
		}
	}
	switch {
	case playing && p.pollCancel == nil:
		ctx, cancel := context.WithCancel(p.watchCtx)
		p.pollCancel = cancel
		interval := config.Duration(p.mprisCfg.PositionInterval)
		p.logger.Debug("mpris: arming position poller",
			log.Duration("interval", interval))
		go p.runPlayingPoller(ctx, interval)
	case !playing && p.pollCancel != nil:
		p.logger.Debug("mpris: disarming position poller")
		p.stopPlayingPollerLocked()
	}
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

// runPlayingPoller re-reads D-Bus state for playing players only, at
// PositionInterval, until ctx is cancelled (pause/stop/removal) or the
// plugin shuts down. Paused players and an empty player list cost zero
// wakeups — that is the entire point: idle desktops stay silent.
func (p *MPRISPlugin) runPlayingPoller(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.mu.RLock()
			playing := make([]*trackedPlayer, 0, len(p.players))
			for _, pl := range p.players {
				if s := p.lastStates[pl.displayName]; s != nil && s.IsPlaying {
					playing = append(playing, pl)
				}
			}
			p.mu.RUnlock()

			for _, pl := range playing {
				state, err := p.playerState(pl.displayName)
				if err != nil {
					continue
				}
				p.mu.RLock()
				last := p.lastStates[pl.displayName]
				p.mu.RUnlock()

				changed := last == nil ||
					state.PlaybackStatus != last.PlaybackStatus ||
					state.Title != last.Title ||
					state.Artist != last.Artist ||
					state.Album != last.Album ||
					state.AlbumArtUrl != last.AlbumArtUrl ||
					state.Volume != last.Volume ||
					state.IsPlaying != last.IsPlaying
				if changed {
					p.storeLocalState(pl.displayName, state)
					p.broadcast(state)
				}
			}
		}
	}
}
