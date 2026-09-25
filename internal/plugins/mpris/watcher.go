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
		// Not fatal: the watcher keeps running so NameOwnerChanged can
		// still pick players up as they appear. Logging matters — this
		// error used to be discarded, leaving an empty tracker with no
		// clue why.
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

	// Watch every name change and filter to MPRIS in the handler. D-Bus
	// match rules have no string-prefix key, and the non-MPRIS signals
	// this lets through are cheap — the handler rejects them on a prefix
	// check before any blocking work.
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
