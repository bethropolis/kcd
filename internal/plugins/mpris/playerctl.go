package mpris

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"go.uber.org/zap"
)

func (p *MPRISPlugin) handleAction(player, action string, seek, setPos *int64, volume *int, shuffle *bool, loopStatus string) {
	busName := "org.mpris.MediaPlayer2." + player
	obj := p.dbus.Object(busName)

	switch action {
	case "Play", "Pause", "PlayPause", "Next", "Previous", "Stop":
		_ = obj.Call("org.mpris.MediaPlayer2.Player."+action, 0)
	case "Seek":
		if seek != nil {
			_ = obj.Call("org.mpris.MediaPlayer2.Player.Seek", 0, *seek)
		}
	case "SetPosition":
		if setPos != nil {
			_ = obj.Call("org.mpris.MediaPlayer2.Player.SetPosition", 0, dbus.ObjectPath("/org/mpris/MediaPlayer2"), *setPos)
		}
	}

	if volume != nil {
		_ = obj.Call("org.freedesktop.DBus.Properties.Set", 0, "org.mpris.MediaPlayer2.Player", "Volume", float64(*volume)/100.0)
	}

	if shuffle != nil {
		_ = obj.Call("org.freedesktop.DBus.Properties.Set", 0, "org.mpris.MediaPlayer2.Player", "Shuffle", *shuffle)
	}

	if loopStatus != "" {
		_ = obj.Call("org.freedesktop.DBus.Properties.Set", 0, "org.mpris.MediaPlayer2.Player", "LoopStatus", loopStatus)
	}

	if state, err := p.playerStateDBus(player); err == nil {
		p.broadcast(state)
	}
}

func (p *MPRISPlugin) playerState(playerName string) (*NowPlaying, error) {
	return p.playerStateDBus(playerName)
}

func (p *MPRISPlugin) playerStateDBus(playerName string) (*NowPlaying, error) {
	busName := "org.mpris.MediaPlayer2." + playerName
	obj := p.dbus.Object(busName)

	var props map[string]dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, "org.mpris.MediaPlayer2.Player").Store(&props); err != nil {
		return nil, fmt.Errorf("get properties: %w", err)
	}

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
				rawArtUrl := artUrlV.Value().(string)
				np.AlbumArtUrl = rawArtUrl

				p.mu.Lock()
				last, exists := p.lastTracks[playerName]
				if !exists || last.rawArtUrl != rawArtUrl {
					p.lastTracks[playerName] = trackIdentity{
						rawArtUrl: rawArtUrl,
						timestamp: time.Now().UnixNano(),
					}
				} else {
					last.timestamp = time.Now().UnixNano()
					p.lastTracks[playerName] = last
				}
				p.mu.Unlock()

				if strings.HasPrefix(rawArtUrl, "file://") {
					np.AlbumArtUrl = fmt.Sprintf("%s?t=%d", rawArtUrl, time.Now().UnixNano())
				}
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
		}
	}

	if np.Title == "" {
		np.Title = "Unknown Media"
	}

	return np, nil
}

func (p *MPRISPlugin) parseOutput(playerName, line string) (*NowPlaying, error) {
	return p.playerStateDBus(playerName)
}

func listPlayers() ([]string, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, nil
	}
	defer conn.Close()

	var names []string
	if err := conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return nil, nil
	}

	type entry struct {
		busName   string
		shortName string
	}
	var all []entry

	for _, name := range names {
		if !strings.HasPrefix(name, "org.mpris.MediaPlayer2.") {
			continue
		}

		short := strings.TrimPrefix(name, "org.mpris.MediaPlayer2.")
		if idx := strings.Index(short, ".instance"); idx != -1 {
			short = short[:idx]
		}
		all = append(all, entry{name, short})
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
	var players []string
	for _, e := range all {
		if hasPlasmaFirefox && strings.HasPrefix(e.busName, "org.mpris.MediaPlayer2.firefox") {
			continue
		}
		if hasPlasmaChrome && strings.HasPrefix(e.busName, "org.mpris.MediaPlayer2.chromium") {
			continue
		}
		if strings.Contains(e.shortName, "playerctld") {
			continue
		}

		if !seen[e.shortName] {
			seen[e.shortName] = true
			players = append(players, e.shortName)
		}
	}

	return players, nil
}