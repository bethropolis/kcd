// Package dbusutil provides shared D-Bus helpers for kcd plugins.
// It is the only internal package that imports godbus/dbus.
package dbusutil

import (
	"github.com/godbus/dbus/v5"
	"go.uber.org/zap"
)

// PauseMPRIS sends Pause to all active MPRIS media players.
// Returns the list of player bus names that were paused so Play can be sent later.
func PauseMPRIS(conn *dbus.Conn, logger *zap.Logger) []string {
	players := activeMPRISPlayers(conn, logger)
	for _, p := range players {
		obj := conn.Object(p, "/org/mpris/MediaPlayer2")
		call := obj.Call("org.mpris.MediaPlayer2.Player.Pause", 0)
		if call.Err != nil {
			logger.Debug("dbusutil: pause failed", zap.String("player", p), zap.Error(call.Err))
		}
	}
	return players
}

// PlayMPRIS sends Play to the named MPRIS players.
func PlayMPRIS(conn *dbus.Conn, players []string, logger *zap.Logger) {
	for _, p := range players {
		obj := conn.Object(p, "/org/mpris/MediaPlayer2")
		call := obj.Call("org.mpris.MediaPlayer2.Player.Play", 0)
		if call.Err != nil {
			logger.Debug("dbusutil: play failed", zap.String("player", p), zap.Error(call.Err))
		}
	}
}

// activeMPRISPlayers returns bus names of all active MPRIS2 players.
func activeMPRISPlayers(conn *dbus.Conn, logger *zap.Logger) []string {
	var names []string
	if err := conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		logger.Warn("dbusutil: ListNames failed", zap.Error(err))
		return nil
	}
	var players []string
	for _, name := range names {
		if len(name) > 23 && name[:23] == "org.mpris.MediaPlayer2." {
			players = append(players, name)
		}
	}
	return players
}
