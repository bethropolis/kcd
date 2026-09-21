package mpris

import (
	"context"
	"time"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/godbus/dbus/v5"
)

const dbusTimeout = 500 * time.Millisecond

func dbusCall(obj dbus.BusObject, method string, args ...interface{}) *dbus.Call {
	ctx, cancel := context.WithTimeout(context.Background(), dbusTimeout)
	defer cancel()
	return obj.CallWithContext(ctx, method, 0, args...)
}

func (p *MPRISPlugin) handleAction(player, action string, seek, setPos *int64, volume *int, shuffle *bool, loopStatus string) {
	pl := p.resolvePlayer(player)
	if pl == nil {
		p.logger.Warn("mpris: cannot resolve player", log.String("player", player))
		return
	}
	obj := p.dbus.Object(pl.busName, "/org/mpris/MediaPlayer2")

	switch action {
	case "Play", "Pause", "PlayPause", "Next", "Previous", "Stop":
		if err := dbusCall(obj, "org.mpris.MediaPlayer2.Player."+action).Err; err != nil {
			p.logger.Warn("mpris: action failed", log.String("action", action), log.Error(err))
		}
	}

	if seek != nil {
		if err := dbusCall(obj, "org.mpris.MediaPlayer2.Player.Seek", (*seek)*1000).Err; err != nil {
			p.logger.Warn("mpris: seek failed", log.Int64("offset", *seek), log.Error(err))
		}
	}

	if setPos != nil {
		currentPosUs := p.getPlayerPosition(pl.busName)
		targetPosUs := (*setPos) * 1000
		seekOffset := targetPosUs - currentPosUs
		if err := dbusCall(obj, "org.mpris.MediaPlayer2.Player.Seek", seekOffset).Err; err != nil {
			p.logger.Warn("mpris: setPosition seek failed", log.Int64("target", *setPos), log.Error(err))
		}
	}

	if volume != nil {
		volF := float64(*volume) / 100.0
		if err := dbusCall(obj, "org.freedesktop.DBus.Properties.Set", "org.mpris.MediaPlayer2.Player", "Volume", dbus.MakeVariant(volF)).Err; err != nil {
			p.logger.Warn("mpris: setVolume failed", log.Float64("volume", volF), log.Error(err))
		}
	}

	if shuffle != nil {
		if err := dbusCall(obj, "org.freedesktop.DBus.Properties.Set", "org.mpris.MediaPlayer2.Player", "Shuffle", dbus.MakeVariant(*shuffle)).Err; err != nil {
			p.logger.Warn("mpris: setShuffle failed", log.Bool("shuffle", *shuffle), log.Error(err))
		}
	}

	if loopStatus != "" {
		if err := dbusCall(obj, "org.freedesktop.DBus.Properties.Set", "org.mpris.MediaPlayer2.Player", "LoopStatus", dbus.MakeVariant(loopStatus)).Err; err != nil {
			p.logger.Warn("mpris: setLoopStatus failed", log.String("loopStatus", loopStatus), log.Error(err))
		}
	}

	if state, err := p.playerState(player); err == nil {
		p.broadcast(state)
	}
}

func (p *MPRISPlugin) getPlayerPosition(busName string) int64 {
	obj := p.dbus.Object(busName, "/org/mpris/MediaPlayer2")
	var v dbus.Variant
	if err := dbusCall(obj, "org.freedesktop.DBus.Properties.Get", "org.mpris.MediaPlayer2.Player", "Position").Store(&v); err != nil {
		return 0
	}
	pos, ok := v.Value().(int64)
	if !ok {
		return 0
	}
	return pos
}
