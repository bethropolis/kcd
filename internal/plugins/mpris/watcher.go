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

	entries, _ := listPlayersDBus(p.dbus)
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

	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
	); err != nil {
		return err
	}

	ch := make(chan *dbus.Signal, 64)
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
