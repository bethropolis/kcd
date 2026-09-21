package mpris

import (
	"time"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/godbus/dbus/v5"
)

// reconcileInterval is how often the D-Bus watcher re-lists player names
// and heals any drift between the bus and p.players. The signal fast-path
// (NameOwnerChanged) handles the common case; this closes the gap when a
// signal is missed, duplicated instance names race add/remove, or the
// watcher connection dropped events during a restart — all of which
// otherwise leave the phone with stale or missing media state forever,
// since the polling loop skips an empty player map entirely.
const reconcileInterval = 15 * time.Second

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
		return
	}
	p.logger.Debug("mpris: reconciling players")

	for _, e := range add {
		var owner string
		if err := conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, e.busName).Store(&owner); err != nil || owner == "" {
			continue
		}
		_ = conn.AddMatchSignal(
			dbus.WithMatchSender(e.busName),
			dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
			dbus.WithMatchMember("PropertiesChanged"),
		)
		_ = conn.AddMatchSignal(
			dbus.WithMatchSender(e.busName),
			dbus.WithMatchInterface("org.mpris.MediaPlayer2.Player"),
			dbus.WithMatchMember("Seeked"),
		)
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
}
