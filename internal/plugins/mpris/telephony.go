package mpris

import (
	"context"

	"github.com/bethropolis/kcd/internal/events"
)

// watchTelephony subscribes to telephony events and pauses/resumes
// local MPRIS players when calls start/end.
func (p *MPRISPlugin) watchTelephony(ctx context.Context) {
	sub := p.bus.Subscribe(events.DefaultSubscriberCap,
		events.TypeTelephonyRinging,
		events.TypeTelephonyTalking,
		events.TypeTelephonyCanceled)
	defer sub.Close()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.C:
			p.handleTelephonyEvent(ev)
		}
	}
}

func (p *MPRISPlugin) handleTelephonyEvent(ev events.Event) {
	switch ev.Type {
	case events.TypeTelephonyRinging, events.TypeTelephonyTalking:
		p.pauseAllPlayers()
	case events.TypeTelephonyCanceled:
		p.resumePausedPlayers()
	}
}

// pauseAllPlayers pauses every currently-playing local MPRIS player.
// It only acts once per call — repeated ringing/talking events are no-ops
// while callPausedPlayers is non-empty.
func (p *MPRISPlugin) pauseAllPlayers() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.callPausedPlayers) > 0 {
		return // already paused for an active call
	}

	for name, pl := range p.players {
		state, err := p.playerStateDBus(pl.busName, name)
		if err != nil {
			continue
		}
		if !state.IsPlaying {
			continue
		}
		obj := p.dbus.Object(pl.busName, "/org/mpris/MediaPlayer2")
		if err := dbusCall(obj, "org.mpris.MediaPlayer2.Player.Pause").Err; err == nil {
			p.callPausedPlayers = append(p.callPausedPlayers, name)
		}
	}
}

// resumePausedPlayers resumes every player that was paused by pauseAllPlayers.
func (p *MPRISPlugin) resumePausedPlayers() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, name := range p.callPausedPlayers {
		pl := p.players[name]
		if pl == nil {
			continue
		}
		obj := p.dbus.Object(pl.busName, "/org/mpris/MediaPlayer2")
		_ = dbusCall(obj, "org.mpris.MediaPlayer2.Player.Play").Err
	}
	p.callPausedPlayers = p.callPausedPlayers[:0]
}
