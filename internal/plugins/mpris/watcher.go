package mpris

import (
	"context"
	"time"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/godbus/dbus/v5"
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
				p.logger.Warn("mpris: D-Bus watcher exited, restarting in 3s", log.Error(err))
				select {
				case <-time.After(3 * time.Second):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
}

func (p *MPRISPlugin) runDBusWatcher(ctx context.Context) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()

	uniqueToDisplay := make(map[string]string)

	entries, err := listPlayersDBus(p.dbus)
	if err != nil {
		// Not fatal: the watcher keeps running so players appearing later are
		// still picked up. Logged, since silence would present as empty tracker.
		p.logger.Warn("mpris: initial player listing failed", log.Error(err))
	}
	for _, e := range entries {
		var owner string
		if err := conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, e.busName).Store(&owner); err == nil {
			uniqueToDisplay[owner] = e.identity
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
			p.addPlayer(e.busName, owner, e.identity, e.shortName)
		}
	}

	// Watch every name change; match rules lack prefix keys, and non-MPRIS
	// signals are cheaply rejected on a prefix check before blocking work.
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
	); err != nil {
		return err
	}

	// Sized for a burst of player churn (browser restarts, several
	// players appearing at once).
	ch := make(chan *dbus.Signal, 256)
	conn.Signal(ch)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-p.reconcileCh:
			p.reconcilePlayers(conn, uniqueToDisplay)
		case sig := <-ch:
			if sig == nil {
				continue
			}

			switch classifySignal(sig.Name) {
			case signalKindNameOwnerChanged:
				p.handleNameOwnerChanged(sig, conn, uniqueToDisplay)
			case signalKindSeeked:
				p.handleSeeked(sig, uniqueToDisplay)
			case signalKindPropertiesChanged:
				p.handlePropertiesChanged(sig, uniqueToDisplay)
			}
		}
	}
}
