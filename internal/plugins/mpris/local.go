package mpris

import (
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

func (p *MPRISPlugin) sendPlayerList(dev device.Sender) error {
	p.mu.RLock()
	displayNames := make([]string, 0, len(p.players))
	for name := range p.players {
		displayNames = append(displayNames, name)
	}
	p.mu.RUnlock()

	if displayNames == nil {
		displayNames = []string{}
	}

	p.logger.Debug("mpris: sending player list", log.Strings("players", displayNames))

	pkt, err := protocol.NewPacket("kdeconnect.mpris", map[string]interface{}{
		"playerList":             displayNames,
		"supportAlbumArtPayload": true,
	})
	if err != nil {
		return err
	}

	go func() {
		for _, name := range displayNames {
			if state, err := p.playerState(name); err == nil {
				p.broadcast(state)
			}
		}
	}()

	return dev.Send(pkt)
}

func (p *MPRISPlugin) sendPlayerListBroadcast() {
	p.mu.RLock()
	displayNames := make([]string, 0, len(p.players))
	for name := range p.players {
		displayNames = append(displayNames, name)
	}
	p.mu.RUnlock()

	if displayNames == nil {
		displayNames = []string{}
	}

	pkt, _ := protocol.NewPacket("kdeconnect.mpris", map[string]interface{}{
		"playerList":             displayNames,
		"supportAlbumArtPayload": true,
	})

	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, dev := range p.devices {
		if dev.IsConnected() {
			_ = dev.Send(pkt)
		}
	}
}

func (p *MPRISPlugin) broadcast(state *NowPlaying) {
	// Stamp the position anchor at send time: Pos was sampled by the
	// caller (signal-time query or poller GetAll) immediately before
	// this broadcast, so receivers can extrapolate the live position as
	// Pos + (nowMs - PosAnchorMs) while IsPlaying.
	//
	// The stamp takes p.mu, and the packet is marshalled from a snapshot
	// taken under the same hold: state aliases the pointer cached in
	// lastStates, and DebugStatus reads its anchor under RLock from the
	// IPC path. Stamping without the mutex races those reads, and
	// marshalling the live pointer races a concurrent broadcast's stamp.
	p.mu.Lock()
	state.PosAnchorMs = time.Now().UnixMilli()
	snapshot := *state
	p.mu.Unlock()

	pkt, err := protocol.NewPacket("kdeconnect.mpris", &snapshot)
	if err != nil {
		return
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, dev := range p.devices {
		if dev.IsConnected() {
			_ = dev.Send(pkt)
		}
	}
}

func (p *MPRISPlugin) addPlayer(busName, uniqueName, displayName, shortName string) {
	p.mu.Lock()
	p.players[displayName] = &trackedPlayer{
		busName:     busName,
		uniqueName:  uniqueName,
		displayName: displayName,
		shortName:   shortName,
	}
	p.mu.Unlock()

	p.logger.Debug("mpris: added player", log.String("displayName", displayName), log.String("busName", busName))

	if state, err := p.playerState(displayName); err == nil {
		p.storeLocalState(displayName, state)
		p.broadcast(state)
	}

	p.sendPlayerListBroadcast()
}

func (p *MPRISPlugin) removePlayer(displayName string) {
	p.mu.Lock()
	delete(p.players, displayName)
	delete(p.lastTracks, displayName)
	delete(p.lastStates, displayName)
	// A removal may take the last playing player with it — disarm the
	// poller so a removed player can't pin the ticker on.
	p.syncPlayingPollerLocked()
	p.mu.Unlock()

	p.logger.Debug("mpris: removed player", log.String("displayName", displayName))

	p.sendPlayerListBroadcast()
}

func (p *MPRISPlugin) resolvePlayer(displayName string) *trackedPlayer {
	if displayName == "" {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, pl := range p.players {
		if pl.displayName == displayName || pl.shortName == displayName || strings.EqualFold(pl.shortName, displayName) {
			return pl
		}
	}
	return nil
}

func (p *MPRISPlugin) DebugStatus() *DebugStatus {
	p.mu.RLock()
	watching := p.watching
	devCount := len(p.devices)
	playerMappings := make(map[string]string, len(p.players))
	playerList := make([]*trackedPlayer, 0, len(p.players))
	for _, pl := range p.players {
		playerMappings[pl.displayName] = pl.busName
		playerList = append(playerList, pl)
	}
	p.mu.RUnlock()

	var players []DebugPlayerInfo
	for _, pl := range playerList {
		info := DebugPlayerInfo{
			DisplayName: pl.displayName,
			BusName:     pl.busName,
			ShortName:   pl.shortName,
		}
		if state, err := p.playerState(pl.displayName); err == nil {
			info.Title = state.Title
			info.Artist = state.Artist
			info.Album = state.Album
			info.PlaybackStatus = state.PlaybackStatus
			info.IsPlaying = state.IsPlaying
			info.Volume = state.Volume
			info.Pos = state.Pos
			info.Length = state.Length
			info.AlbumArtUrl = state.AlbumArtUrl
			info.CanSeek = state.CanSeek
			info.CanGoNext = state.CanGoNext
			info.CanGoPrevious = state.CanGoPrevious
			info.CanPlay = state.CanPlay
			info.CanPause = state.CanPause
			// Anchor from the last broadcast, not this query: DebugStatus
			// re-reads D-Bus live, but clients extrapolate from the
			// cached anchor stamped at send time.
			p.mu.RLock()
			if cached := p.lastStates[pl.displayName]; cached != nil {
				info.PosAnchorMs = cached.PosAnchorMs
			}
			p.mu.RUnlock()
		} else {
			info.Error = err.Error()
		}
		players = append(players, info)
	}

	return &DebugStatus{
		WatcherRunning: watching,
		DeviceCount:    devCount,
		Players:        players,
		PlayerMappings: playerMappings,
	}
}
