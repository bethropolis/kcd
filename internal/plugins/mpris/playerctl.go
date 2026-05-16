package mpris

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"go.uber.org/zap"
)

func (p *MPRISPlugin) handleAction(player, action string, seek, setPos *int64, volume *int, shuffle *bool, loopStatus string) {
	busName := p.resolvePlayerBus(player)
	if busName == "" {
		p.logger.Warn("mpris: cannot resolve player bus name", zap.String("player", player))
		return
	}
	obj := p.dbus.Object(busName, "/org/mpris/MediaPlayer2")

	switch action {
	case "Play", "Pause", "PlayPause", "Next", "Previous", "Stop":
		if err := obj.Call("org.mpris.MediaPlayer2.Player."+action, 0).Err; err != nil {
			p.logger.Debug("mpris: action failed", zap.String("action", action), zap.Error(err))
		}
	case "Seek":
		if seek != nil {
			if err := obj.Call("org.mpris.MediaPlayer2.Player.Seek", 0, *seek).Err; err != nil {
				p.logger.Debug("mpris: seek failed", zap.Int64("offset", *seek), zap.Error(err))
			}
		}
	case "SetPosition":
		if setPos != nil {
			currentPosUs := p.getPlayerPosition(busName)
			targetPosUs := (*setPos) * 1000
			seekOffset := targetPosUs - currentPosUs
			if err := obj.Call("org.mpris.MediaPlayer2.Player.Seek", 0, seekOffset).Err; err != nil {
				p.logger.Debug("mpris: setPosition seek failed", zap.Int64("target", *setPos), zap.Error(err))
			}
		}
	}

	if volume != nil {
		volF := float64(*volume) / 100.0
		if err := obj.Call("org.freedesktop.DBus.Properties.Set", 0, "org.mpris.MediaPlayer2.Player", "Volume", volF).Err; err != nil {
			p.logger.Debug("mpris: setVolume failed", zap.Float64("volume", volF), zap.Error(err))
		} else {
			p.mu.Lock()
			p.prevVolume[player] = volF
			p.mu.Unlock()
		}
	}

	if shuffle != nil {
		if err := obj.Call("org.freedesktop.DBus.Properties.Set", 0, "org.mpris.MediaPlayer2.Player", "Shuffle", *shuffle).Err; err != nil {
			p.logger.Debug("mpris: setShuffle failed", zap.Bool("shuffle", *shuffle), zap.Error(err))
		}
	}

	if loopStatus != "" {
		if err := obj.Call("org.freedesktop.DBus.Properties.Set", 0, "org.mpris.MediaPlayer2.Player", "LoopStatus", loopStatus).Err; err != nil {
			p.logger.Debug("mpris: setLoopStatus failed", zap.String("loopStatus", loopStatus), zap.Error(err))
		}
	}

	// State updates are handled by the D-Bus PropertiesChanged signal watcher.
	// No need to manually poll after actions — the player will emit signals.
}

func (p *MPRISPlugin) getPlayerPosition(busName string) int64 {
	obj := p.dbus.Object(busName, "/org/mpris/MediaPlayer2")
	var pos int64
	if err := obj.Call("org.mpris.MediaPlayer2.Player.Get", 0, "org.mpris.MediaPlayer2.Player", "Position").Store(&pos); err != nil {
		return 0
	}
	return pos
}

func (p *MPRISPlugin) playerState(playerName string) (*NowPlaying, error) {
	return p.playerStateDBus(playerName)
}

func (p *MPRISPlugin) playerStateDBus(playerName string) (*NowPlaying, error) {
	busName := p.resolvePlayerBus(playerName)
	if busName == "" {
		return nil, fmt.Errorf("mpris: cannot resolve player bus name for %q", playerName)
	}
	obj := p.dbus.Object(busName, "/org/mpris/MediaPlayer2")

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
				trackChanged := !exists || last.rawArtUrl != rawArtUrl
				if trackChanged {
					p.lastTracks[playerName] = trackIdentity{
						rawArtUrl: rawArtUrl,
						timestamp: time.Now().UnixNano(),
					}
				}
				p.mu.Unlock()

				// Only add cache-buster when track actually changes
				if strings.HasPrefix(rawArtUrl, "file://") && trackChanged {
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

func (p *MPRISPlugin) getPlayerArtUrl(busName string) string {
	obj := p.dbus.Object(busName, "/org/mpris/MediaPlayer2")
	var meta map[string]dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2.Player", "Metadata").Store(&meta); err != nil {
		return ""
	}
	if v, ok := meta["mpris:artUrl"]; ok {
		return v.Value().(string)
	}
	return ""
}

// resolvePlayerBus maps a display name back to its D-Bus bus name.
func (p *MPRISPlugin) resolvePlayerBus(displayName string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Try exact match first
	if bus, ok := p.playerNameToBus[displayName]; ok {
		return bus
	}

	// Try matching by short name (fallback for when display name differs)
	for bus, storedDisplayName := range p.playerNameToBus {
		_ = storedDisplayName
		short := strings.TrimPrefix(bus, "org.mpris.MediaPlayer2.")
		if idx := strings.Index(short, ".instance"); idx != -1 {
			short = short[:idx]
		}
		if short == displayName {
			return bus
		}
	}

	// Try matching by prefix (e.g. "VLC" → "org.mpris.MediaPlayer2.vlc")
	lowerDisplayName := strings.ToLower(displayName)
	for bus := range p.playerNameToBus {
		short := strings.TrimPrefix(bus, "org.mpris.MediaPlayer2.")
		if idx := strings.Index(short, ".instance"); idx != -1 {
			short = short[:idx]
		}
		if strings.Contains(strings.ToLower(short), lowerDisplayName) || strings.Contains(lowerDisplayName, strings.ToLower(short)) {
			return bus
		}
	}

	p.logger.Debug("mpris: failed to resolve player bus", zap.String("displayName", displayName))
	return ""
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

	// Resolve Identity for each player
	for i := range all {
		obj := conn.Object(all[i].busName, "/org/mpris/MediaPlayer2")
		var identity string
		if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2", "Identity").Store(&identity); err == nil && identity != "" {
			all[i].identity = identity
		} else {
			all[i].identity = all[i].shortName
		}
	}

	// Detect plasma-browser-integration wrappers
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

	// Build deduplicated display names
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
