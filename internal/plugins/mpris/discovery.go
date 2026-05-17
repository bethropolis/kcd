package mpris

import (
	"fmt"
	"strings"

	"github.com/godbus/dbus/v5"
)

type playerEntry struct {
	busName   string
	shortName string
	identity  string
}

func listPlayersDBus(conn *dbus.Conn) ([]playerEntry, error) {
	if conn == nil {
		return nil, nil
	}

	var names []string
	if err := conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return nil, err
	}

	var all []playerEntry
	for _, name := range names {
		if !strings.HasPrefix(name, "org.mpris.MediaPlayer2.") {
			continue
		}
		if strings.HasPrefix(name, "org.mpris.MediaPlayer2.kdeconnect.") {
			continue
		}
		if name == "org.mpris.MediaPlayer2.playerctld" {
			continue
		}

		short := strings.TrimPrefix(name, "org.mpris.MediaPlayer2.")
		if idx := strings.Index(short, ".instance"); idx != -1 {
			short = short[:idx]
		}
		all = append(all, playerEntry{name, short, ""})
	}

	for i := range all {
		obj := conn.Object(all[i].busName, "/org/mpris/MediaPlayer2")
		var identity string
		if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2", "Identity").Store(&identity); err == nil && identity != "" {
			all[i].identity = identity
		} else {
			all[i].identity = all[i].shortName
		}
	}

	hasPlasmaFirefox := false
	hasPlasmaChrome := false
	for _, e := range all {
		if strings.HasPrefix(e.busName, "org.mpris.MediaPlayer2.plasma-browser-integration") {
			if strings.Contains(e.busName, "Firefox") {
				hasPlasmaFirefox = true
			}
			if strings.Contains(e.busName, "Chrome") || strings.Contains(e.busName, "Chromium") {
				hasPlasmaChrome = true
			}
		}
	}

	seen := make(map[string]bool)
	var result []playerEntry
	for _, e := range all {
		if hasPlasmaFirefox && strings.HasPrefix(e.busName, "org.mpris.MediaPlayer2.firefox") {
			continue
		}
		if hasPlasmaChrome && strings.HasPrefix(e.busName, "org.mpris.MediaPlayer2.chromium") {
			continue
		}

		displayName := e.identity
		baseName := displayName
		for n := 2; seen[displayName]; n++ {
			displayName = fmt.Sprintf("%s [%d]", baseName, n)
		}
		seen[displayName] = true
		e.identity = displayName
		result = append(result, e)
	}

	return result, nil
}

func resolveIdentity(conn *dbus.Conn, busName string) *playerEntry {
	short := strings.TrimPrefix(busName, "org.mpris.MediaPlayer2.")
	if idx := strings.Index(short, ".instance"); idx != -1 {
		short = short[:idx]
	}

	obj := conn.Object(busName, "/org/mpris/MediaPlayer2")
	var identity string
	if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2", "Identity").Store(&identity); err == nil && identity != "" {
		return &playerEntry{busName: busName, shortName: short, identity: identity}
	}
	return &playerEntry{busName: busName, shortName: short, identity: short}
}
