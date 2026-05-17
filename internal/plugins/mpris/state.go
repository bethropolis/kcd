package mpris

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/godbus/dbus/v5"
)

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
