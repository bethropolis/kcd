package mpris

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/plugin"
	"go.uber.org/zap"
)

func (p *MPRISPlugin) handleAction(player, action string, seek, setPos *int64, volume *int, shuffle *bool, loopStatus string) {
	var args []string

	switch action {
	case "Play", "Pause", "PlayPause", "Next", "Previous", "Stop":
		args = []string{"-p", player, strings.ToLower(action)}

	case "Seek":
		if seek != nil {
			secs := float64(*seek) / 1_000_000.0
			args = []string{"-p", player, "position", fmt.Sprintf("%+.6f", secs)}
		}

	case "SetPosition":
		if setPos != nil {
			secs := float64(*setPos) / 1_000_000.0
			args = []string{"-p", player, "position", fmt.Sprintf("%.6f", secs)}
		}
	}

	// Handle Volume (if present)
	if volume != nil {
		args = []string{"-p", player, "volume", fmt.Sprintf("%.2f", float64(*volume)/100.0)}
	}

	// Handle Shuffle (if present)
	if shuffle != nil {
		state := "Off"
		if *shuffle {
			state = "On"
		}
		args = []string{"-p", player, "shuffle", state}
	}

	// Handle Loop (if present)
	if loopStatus != "" {
		args = []string{"-p", player, "loop", loopStatus}
	}

	if len(args) == 0 {
		return
	}

	if err := plugin.NewPlayerctlCmd(nil, args...).Run(); err != nil {
		p.logger.Debug("mpris: action failed",
			zap.String("player", player),
			zap.Strings("args", args),
			zap.Error(err),
		)
	}

	// Immediately read and broadcast the new state so the phone UI updates
	if state, err := p.playerState(player); err == nil {
		p.broadcast(state)
	}
}

func (p *MPRISPlugin) playerState(playerName string) (*NowPlaying, error) {
	out, err := plugin.NewPlayerctlCmd(nil, "-p", playerName, "metadata", "--format", outputFormat).Output()
	if err != nil {
		return nil, fmt.Errorf("playerctl metadata: %w", err)
	}
	np, err := p.parseOutput(playerName, strings.TrimSpace(string(out)))
	if err != nil {
		return nil, err
	}

	// Fetch shuffle/loop status separately as they aren't in metadata
	sOut, _ := plugin.NewPlayerctlCmd(nil, "-p", playerName, "shuffle").Output()
	isShuffle := strings.TrimSpace(string(sOut)) == "On"
	np.Shuffle = &isShuffle

	lOut, _ := plugin.NewPlayerctlCmd(nil, "-p", playerName, "loop").Output()
	np.LoopStatus = strings.TrimSpace(string(lOut))

	return np, nil
}

func (p *MPRISPlugin) parseOutput(playerName, line string) (*NowPlaying, error) {
	// Clean up playerctl output
	line = strings.ReplaceAll(line, "<no value>", "")

	parts := strings.Split(line, "|||")
	if len(parts) < 15 {
		return nil, fmt.Errorf("unexpected playerctl output: %q", line)
	}

	length, err := strconv.ParseInt(parts[6], 10, 64)
	if err != nil || length == 0 {
		length = -1
	}
	pos, _ := strconv.ParseInt(parts[7], 10, 64)
	volF, _ := strconv.ParseFloat(parts[8], 64)

	title := parts[2]
	artist := parts[3]
	album := parts[4]
	rawArtUrl := parts[5]
	trackUrl := parts[14]

	// Metadata Fallback: If title is empty but we have a local URL, use filename
	if title == "" && strings.HasPrefix(trackUrl, "file://") {
		cleanUrl := strings.TrimPrefix(trackUrl, "file://")
		if unescaped, err := url.PathUnescape(cleanUrl); err == nil {
			title = filepath.Base(unescaped)
			if album == "" {
				album = filepath.Base(filepath.Dir(unescaped))
			}
		}
	}

	// Determine if the track identity has changed for cache-busting
	p.mu.Lock()
	last, exists := p.lastTracks[playerName]

	if !exists || last.title != title || last.artist != artist || last.album != album || last.rawArtUrl != rawArtUrl {
		last = trackIdentity{
			title:     title,
			artist:    artist,
			album:     album,
			rawArtUrl: rawArtUrl,
			timestamp: time.Now().UnixNano(),
		}
		p.lastTracks[playerName] = last
	}
	p.mu.Unlock()

	artUrl := rawArtUrl
	if strings.HasPrefix(artUrl, "file://") {
		artUrl = fmt.Sprintf("%s?t=%d", artUrl, last.timestamp)
	}

	np := &NowPlaying{
		Player:         playerName,
		PlaybackStatus: parts[1],
		IsPlaying:      parts[1] == "Playing",
		Title:          title,
		Artist:         artist,
		Album:          album,
		AlbumArtUrl:    artUrl,
		Url:            trackUrl,
		Length:         length / 1000,
		Pos:            pos / 1000,
		Volume:         int(volF * 100),
		CanControl:     true,
		CanGoNext:      parts[11] == "true",
		CanGoPrevious:  parts[12] == "true",
		CanPause:       parts[10] == "true",
		CanPlay:        parts[9] == "true",
		CanSeek:        parts[13] == "true",
	}

	if np.Title == "" {
		np.Title = "Unknown Media"
	}

	return np, nil
}

// listPlayers reliably reads the D-Bus registry to find active players,
// even if they are currently paused or stopped.
func listPlayers() ([]string, error) {
	out, err := plugin.NewPlayerctlCmd(nil, "-l").Output()
	if err != nil {
		return nil, nil
	}

	var players []string
	seen := make(map[string]bool)

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// playerctl -l outputs names like "firefox.instance1234"
		// Strip the prefix/suffix to match `{{playerName}}` which the watcher outputs.
		name := strings.TrimPrefix(line, "org.mpris.MediaPlayer2.")
		if idx := strings.Index(name, ".instance"); idx != -1 {
			name = name[:idx]
		}

		if !seen[name] {
			seen[name] = true
			players = append(players, name)
		}
	}

	return players, nil
}
