package mpris

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/godbus/dbus/v5"
	"go.uber.org/zap"
)

func dbusCall(obj dbus.BusObject, method string, args ...interface{}) *dbus.Call {
	ctx, cancel := context.WithTimeout(context.Background(), dbusTimeout)
	defer cancel()
	return obj.CallWithContext(ctx, method, 0, args...)
}

func (p *MPRISPlugin) handleAction(player, action string, seek, setPos *int64, volume *int, shuffle *bool, loopStatus string) {
	pl := p.resolvePlayer(player)
	if pl == nil {
		p.logger.Warn("mpris: cannot resolve player", zap.String("player", player))
		return
	}
	obj := p.dbus.Object(pl.busName, "/org/mpris/MediaPlayer2")

	switch action {
	case "Play", "Pause", "PlayPause", "Next", "Previous", "Stop":
		if err := dbusCall(obj, "org.mpris.MediaPlayer2.Player."+action).Err; err != nil {
			p.logger.Debug("mpris: action failed", zap.String("action", action), zap.Error(err))
		}
	case "Seek":
		if seek != nil {
			if err := dbusCall(obj, "org.mpris.MediaPlayer2.Player.Seek", *seek).Err; err != nil {
				p.logger.Debug("mpris: seek failed", zap.Int64("offset", *seek), zap.Error(err))
			}
		}
	case "SetPosition":
		if setPos != nil {
			currentPosUs := p.getPlayerPosition(pl.busName)
			targetPosUs := (*setPos) * 1000
			seekOffset := targetPosUs - currentPosUs
			if err := dbusCall(obj, "org.mpris.MediaPlayer2.Player.Seek", seekOffset).Err; err != nil {
				p.logger.Debug("mpris: setPosition seek failed", zap.Int64("target", *setPos), zap.Error(err))
			}
		}
	}

	if volume != nil {
		volF := float64(*volume) / 100.0
		if err := dbusCall(obj, "org.freedesktop.DBus.Properties.Set", "org.mpris.MediaPlayer2.Player", "Volume", volF).Err; err != nil {
			p.logger.Debug("mpris: setVolume failed", zap.Float64("volume", volF), zap.Error(err))
		} else {
			p.prevVolume = *volume
		}
	}

	if shuffle != nil {
		if err := dbusCall(obj, "org.freedesktop.DBus.Properties.Set", "org.mpris.MediaPlayer2.Player", "Shuffle", *shuffle).Err; err != nil {
			p.logger.Debug("mpris: setShuffle failed", zap.Bool("shuffle", *shuffle), zap.Error(err))
		}
	}

	if loopStatus != "" {
		if err := dbusCall(obj, "org.freedesktop.DBus.Properties.Set", "org.mpris.MediaPlayer2.Player", "LoopStatus", loopStatus).Err; err != nil {
			p.logger.Debug("mpris: setLoopStatus failed", zap.String("loopStatus", loopStatus), zap.Error(err))
		}
	}
}

func (p *MPRISPlugin) getPlayerPosition(busName string) int64 {
	obj := p.dbus.Object(busName, "/org/mpris/MediaPlayer2")
	var pos int64
	if err := dbusCall(obj, "org.mpris.MediaPlayer2.Player.Get", "Position").Store(&pos); err != nil {
		return 0
	}
	return pos
}

func (p *MPRISPlugin) playerState(playerName string) (*NowPlaying, error) {
	pl := p.resolvePlayer(playerName)
	if pl == nil {
		return nil, fmt.Errorf("mpris: cannot resolve player %q", playerName)
	}
	return p.playerStateDBus(pl.busName, playerName)
}

func (p *MPRISPlugin) playerStateDBus(busName, playerName string) (*NowPlaying, error) {
	obj := p.dbus.Object(busName, "/org/mpris/MediaPlayer2")

	var props map[string]dbus.Variant
	if err := dbusCall(obj, "org.freedesktop.DBus.Properties.GetAll", "org.mpris.MediaPlayer2.Player").Store(&props); err != nil {
		return nil, fmt.Errorf("get properties: %w", err)
	}

	return buildNowPlaying(props, playerName), nil
}

func buildNowPlaying(props map[string]dbus.Variant, playerName string) *NowPlaying {
	np := &NowPlaying{
		Player:     playerName,
		CanControl: true,
	}

	if v, ok := props["PlaybackStatus"]; ok {
		np.PlaybackStatus = v.Value().(string)
		np.IsPlaying = np.PlaybackStatus == "Playing"
	}

	if v, ok := props["Volume"]; ok {
		np.Volume = int(v.Value().(float64) * 100)
	}

	if v, ok := props["Position"]; ok {
		np.Pos = v.Value().(int64) / 1000
	}

	if v, ok := props["Shuffle"]; ok {
		shuffle := v.Value().(bool)
		np.Shuffle = &shuffle
	}

	if v, ok := props["LoopStatus"]; ok {
		np.LoopStatus = v.Value().(string)
	}

	if v, ok := props["CanSeek"]; ok {
		np.CanSeek = v.Value().(bool)
	}

	if v, ok := props["CanGoNext"]; ok {
		np.CanGoNext = v.Value().(bool)
	}

	if v, ok := props["CanGoPrevious"]; ok {
		np.CanGoPrevious = v.Value().(bool)
	}

	if v, ok := props["CanPause"]; ok {
		np.CanPause = v.Value().(bool)
	}

	if v, ok := props["CanPlay"]; ok {
		np.CanPlay = v.Value().(bool)
	}

	if v, ok := props["Metadata"]; ok {
		if meta, ok := v.Value().(map[string]dbus.Variant); ok {
			fillMetadataFromMap(np, meta)
		}
	}

	return np
}

func fillMetadataFromMap(np *NowPlaying, meta map[string]dbus.Variant) {
	if tv, ok := meta["xesam:title"]; ok {
		np.Title = tv.Value().(string)
	}
	if av, ok := meta["xesam:artist"]; ok {
		if artists, ok := av.Value().([]string); ok {
			np.Artist = strings.Join(artists, ", ")
		}
	}
	if alv, ok := meta["xesam:album"]; ok {
		np.Album = alv.Value().(string)
	}
	if artUrlV, ok := meta["mpris:artUrl"]; ok {
		np.AlbumArtUrl = artUrlV.Value().(string)
	}
	if lv, ok := meta["mpris:length"]; ok {
		if length, ok := lv.Value().(int64); ok {
			np.Length = length / 1000
		}
	}
	if uv, ok := meta["xesam:url"]; ok {
		if trackUrl, ok := uv.Value().(string); ok {
			np.Url = trackUrl
			if np.Title == "" && strings.HasPrefix(trackUrl, "file://") {
				if unescaped, err := url.PathUnescape(strings.TrimPrefix(trackUrl, "file://")); err == nil {
					np.Title = filepath.Base(unescaped)
					if np.Album == "" {
						np.Album = filepath.Base(filepath.Dir(unescaped))
					}
				}
			}
		}
	}

	if np.Title == "" {
		np.Title = "Unknown Media"
	}
}

func fillChangedProps(np *NowPlaying, changed map[string]dbus.Variant) bool {
	somethingChanged := false

	if metaV, ok := changed["Metadata"]; ok {
		if meta, ok := metaV.Value().(map[string]dbus.Variant); ok {
			fillMetadataFromMap(np, meta)
			somethingChanged = true
		}
	}

	if v, ok := changed["PlaybackStatus"]; ok {
		np.PlaybackStatus = v.Value().(string)
		np.IsPlaying = np.PlaybackStatus == "Playing"
		somethingChanged = true
	}

	if v, ok := changed["Volume"]; ok {
		np.Volume = int(v.Value().(float64) * 100)
		somethingChanged = true
	}

	if v, ok := changed["CanPlay"]; ok {
		np.CanPlay = v.Value().(bool)
		somethingChanged = true
	}
	if v, ok := changed["CanPause"]; ok {
		np.CanPause = v.Value().(bool)
		somethingChanged = true
	}
	if v, ok := changed["CanGoNext"]; ok {
		np.CanGoNext = v.Value().(bool)
		somethingChanged = true
	}
	if v, ok := changed["CanGoPrevious"]; ok {
		np.CanGoPrevious = v.Value().(bool)
		somethingChanged = true
	}
	if v, ok := changed["CanSeek"]; ok {
		np.CanSeek = v.Value().(bool)
		somethingChanged = true
	}

	if v, ok := changed["LoopStatus"]; ok {
		np.LoopStatus = v.Value().(string)
		somethingChanged = true
	}
	if v, ok := changed["Shuffle"]; ok {
		shuffle := v.Value().(bool)
		np.Shuffle = &shuffle
		somethingChanged = true
	}

	return somethingChanged
}

func (p *MPRISPlugin) getPlayerArtUrl(busName string) string {
	obj := p.dbus.Object(busName, "/org/mpris/MediaPlayer2")
	var meta map[string]dbus.Variant
	if err := dbusCall(obj, "org.freedesktop.DBus.Properties.Get", "org.mpris.MediaPlayer2.Player", "Metadata").Store(&meta); err != nil {
		return ""
	}
	if v, ok := meta["mpris:artUrl"]; ok {
		return v.Value().(string)
	}
	return ""
}

func (p *MPRISPlugin) queryPositionAndCanSeek(pl *trackedPlayer) (pos int64, canSeek bool) {
	obj := p.dbus.Object(pl.busName, "/org/mpris/MediaPlayer2")

	var position int64
	if err := dbusCall(obj, "org.mpris.MediaPlayer2.Player.Get", "Position").Store(&position); err == nil {
		pos = position / 1000
	}

	var seekable bool
	if err := dbusCall(obj, "org.freedesktop.DBus.Properties.Get", "org.mpris.MediaPlayer2.Player", "CanSeek").Store(&seekable); err == nil {
		canSeek = seekable
	}

	return
}

type playerEntry struct {
	busName   string
	shortName string
	identity  string
}

func listPlayersDBus(conn *dbus.Conn, logger *zap.Logger) ([]playerEntry, error) {
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

	if logger != nil {
		var names []string
		for _, e := range result {
			names = append(names, e.identity+" ("+e.shortName+")")
		}
		logger.Debug("mpris: discovered players", zap.Strings("players", names))
	}

	return result, nil
}

func listPlayers() ([]string, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, nil
	}
	defer conn.Close()

	entries, err := listPlayersDBus(conn, nil)
	if err != nil {
		return nil, nil
	}

	var names []string
	for _, e := range entries {
		names = append(names, e.identity)
	}
	return names, nil
}
