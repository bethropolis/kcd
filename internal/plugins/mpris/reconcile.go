package mpris

import (
	"github.com/bethropolis/kcd/internal/log"
	"github.com/godbus/dbus/v5"
)

// diffTracked compares live bus names against tracked players.
// tracked maps busName -> displayName. It returns entries present on the
// bus but untracked (to add) and bus names tracked but no longer owned
// (to drop). Pure function for testability; all D-Bus I/O stays in
// reconcilePlayers.
func diffTracked(entries []playerEntry, tracked map[string]string) (add []playerEntry, drop []string) {
	owned := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		owned[e.busName] = struct{}{}
		if _, ok := tracked[e.busName]; !ok {
			add = append(add, e)
		}
	}
	for busName := range tracked {
		if _, ok := owned[busName]; !ok {
			drop = append(drop, busName)
		}
	}
	return add, drop
}

// reconcilePlayers heals drift between the session bus and p.players.
// Missing players are added through the same path as the signal handler
// (match rules + broadcast so the phone learns about them); ghosts are
// removed. A removal only fires if the tracked entry still points at the
// dead bus name, so a same-named live player (duplicate Identity, e.g. two
// Firefox profiles) is never evicted by its dead twin.
func (p *MPRISPlugin) reconcilePlayers(conn *dbus.Conn, uniqueToDisplay map[string]string) {
	if p.dbus == nil || conn == nil {
		return
	}
	entries, err := listPlayersDBus(p.dbus)
	if err != nil {
		// Transient failure: never prune on a failed listing.
		p.logger.Debug("mpris: reconcile listing failed", log.Error(err))
		return
	}

	p.mu.RLock()
	tracked := make(map[string]string, len(p.players))
	for display, pl := range p.players {
		tracked[pl.busName] = display
	}
	p.mu.RUnlock()

	add, drop := diffTracked(entries, tracked)
	if len(add) == 0 && len(drop) == 0 {
		// Names agree, but state may still be stale (a missed Play
		// signal disarms the position poller with nothing left to
		// correct it). Heal that too.
		p.refreshTrackedStates()
		return
	}
	p.logger.Debug("mpris: reconciling players")

	for _, e := range add {
		var owner string
		if err := conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, e.busName).Store(&owner); err != nil || owner == "" {
			continue
		}
		if err := conn.AddMatchSignal(
			dbus.WithMatchSender(e.busName),
			dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
			dbus.WithMatchMember("PropertiesChanged"),
		); err != nil {
			p.logger.Warn("mpris: match rule rejected", log.String("busName", e.busName), log.Error(err))
		}
		if err := conn.AddMatchSignal(
			dbus.WithMatchSender(e.busName),
			dbus.WithMatchInterface("org.mpris.MediaPlayer2.Player"),
			dbus.WithMatchMember("Seeked"),
		); err != nil {
			p.logger.Warn("mpris: match rule rejected", log.String("busName", e.busName), log.Error(err))
		}
		uniqueToDisplay[owner] = e.identity
		p.addPlayer(e.busName, owner, e.identity, e.shortName)
	}

	for _, busName := range drop {
		p.mu.RLock()
		display, ok := tracked[busName]
		current, stillTracked := p.players[display]
		p.mu.RUnlock()
		if !ok || !stillTracked || current.busName != busName {
			continue
		}
		p.removePlayer(display)
		// Drop stale owner mappings only when no same-named live player
		// remains; otherwise the live owner's routing must stay intact.
		p.mu.RLock()
		_, displayAlive := p.players[display]
		p.mu.RUnlock()
		if !displayAlive {
			for owner, mapped := range uniqueToDisplay {
				if mapped == display {
					delete(uniqueToDisplay, owner)
				}
			}
		}
	}
	p.refreshTrackedStates()
}

// refreshTrackedStates re-reads D-Bus state for every tracked player,
// storing and broadcasting changes. Reconcile heals names; this heals
// STATE: without it a missed or stale Play signal leaves the position
// poller disarmed (or a pause missed leaves it armed) with no further
// signal arriving to correct it. Triggers are rare (unknown senders,
// explicit queries, connects), so the per-player GetAll is event-driven
// cost — never a standing timer. Read failures change nothing.
func (p *MPRISPlugin) refreshTrackedStates() {
	if p.dbus == nil {
		return
	}
	p.mu.RLock()
	names := make([]string, 0, len(p.players))
	for display := range p.players {
		names = append(names, display)
	}
	p.mu.RUnlock()

	for _, display := range names {
		state, err := p.playerState(display)
		if err != nil {
			continue
		}
		p.mu.RLock()
		last := p.lastStates[display]
		p.mu.RUnlock()
		if localStateChanged(state, last) {
			p.storeLocalState(display, state)
			p.broadcast(state)
		}
	}
}

// localStateChanged reports whether a fresh read differs from the cached
// state on any broadcasted field. A nil cache always counts as changed.
func localStateChanged(state, last *NowPlaying) bool {
	return last == nil ||
		state.PlaybackStatus != last.PlaybackStatus ||
		state.Title != last.Title ||
		state.Artist != last.Artist ||
		state.Album != last.Album ||
		state.AlbumArtUrl != last.AlbumArtUrl ||
		state.Volume != last.Volume ||
		state.IsPlaying != last.IsPlaying
}
