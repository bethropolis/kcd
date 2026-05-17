package mpris

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
	"go.uber.org/zap"
)

func (p *MPRISPlugin) startWatcher(ctx context.Context) {
	if p.dbus == nil {
		p.logger.Warn("mpris: D-Bus not available, cannot start watcher")
		return
	}

	go func() {
		for {
			if ctx.Err() != nil {
				return
			}
			if err := p.runDBusWatcher(ctx); err != nil && ctx.Err() == nil {
				p.logger.Warn("mpris: D-Bus watcher exited, restarting in 3s", zap.Error(err))
				select {
				case <-time.After(3 * time.Second):
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	go p.runPollingLoop(ctx)
}

func (p *MPRISPlugin) runPollingLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastHash string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.mu.RLock()
			players := make([]*trackedPlayer, 0, len(p.players))
			for _, pl := range p.players {
				players = append(players, pl)
			}
			p.mu.RUnlock()

			if len(players) == 0 {
				continue
			}

			var hash string
			for _, pl := range players {
				if state, err := p.playerState(pl.displayName); err == nil {
					p.mu.RLock()
					last := p.lastStates[pl.displayName]
					p.mu.RUnlock()

					changed := state.PlaybackStatus != last.PlaybackStatus ||
						state.Title != last.Title ||
						state.Artist != last.Artist ||
						state.Album != last.Album ||
						state.AlbumArtUrl != last.AlbumArtUrl ||
						state.Volume != last.Volume ||
						state.IsPlaying != last.IsPlaying

					if changed {
						p.mu.Lock()
						p.lastStates[pl.displayName] = state
						p.mu.Unlock()
						p.broadcast(state)
					}

					hash += pl.displayName + state.PlaybackStatus + state.Title
				}
			}

			if hash != lastHash {
				lastHash = hash
			}
		}
	}
}

func (p *MPRISPlugin) runDBusWatcher(ctx context.Context) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()

	uniqueToDisplay := make(map[string]string)

	entries, _ := listPlayersDBus(p.dbus)
	for _, e := range entries {
		var owner string
		if err := conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, e.busName).Store(&owner); err == nil {
			uniqueToDisplay[owner] = e.identity
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
			p.addPlayer(e.busName, owner, e.identity, e.shortName)
		}
	}

	_ = conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
	)

	ch := make(chan *dbus.Signal, 64)
	conn.Signal(ch)

	for {
		select {
		case <-ctx.Done():
			return nil
		case sig := <-ch:
			if sig == nil {
				continue
			}

			switch sig.Name {
			case "NameOwnerChanged":
				p.handleNameOwnerChanged(sig, conn, uniqueToDisplay)
			case "Seeked":
				p.handleSeeked(sig, uniqueToDisplay)
			case "PropertiesChanged":
				p.handlePropertiesChanged(sig, uniqueToDisplay)
			}
		}
	}
}
