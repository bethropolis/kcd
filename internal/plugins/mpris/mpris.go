package mpris

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/plugins/share"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/godbus/dbus/v5"
	"go.uber.org/zap"
)

type trackedPlayer struct {
	busName     string
	uniqueName  string
	displayName string
	shortName   string
}

type MPRISPlugin struct {
	tlsConfig *tls.Config
	logger    *zap.Logger
	mu        sync.RWMutex
	devices   map[string]device.Sender
	dbus      *dbus.Conn

	watchCancel context.CancelFunc
	watching    bool

	players     map[string]*trackedPlayer
	lastTracks  map[string]trackIdentity
	lastStates  map[string]*NowPlaying
	artRequests map[string]time.Time
}

type trackIdentity struct {
	rawArtUrl string
}

func NewMPRISPlugin(tlsConfig *tls.Config, logger *zap.Logger) *MPRISPlugin {
	dbusConn, err := dbus.ConnectSessionBus()
	if err != nil {
		logger.Warn("mpris: failed to connect to D-Bus session bus", zap.Error(err))
	} else {
		logger.Info("mpris: connected to D-Bus session bus")
	}

	p := &MPRISPlugin{
		tlsConfig:   tlsConfig,
		logger:      logger.With(zap.String("plugin", "mpris")),
		dbus:        dbusConn,
		devices:     make(map[string]device.Sender),
		players:     make(map[string]*trackedPlayer),
		lastTracks:  make(map[string]trackIdentity),
		lastStates:  make(map[string]*NowPlaying),
		artRequests: make(map[string]time.Time),
	}

	// Start the watcher immediately (like C++ does in constructor).
	// Devices are registered lazily as packets arrive.
	watchCtx, cancel := context.WithCancel(context.Background())
	p.watchCancel = cancel
	p.watching = true
	p.startWatcher(watchCtx)

	return p
}

func (p *MPRISPlugin) Name() string           { return "MPRIS" }
func (p *MPRISPlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *MPRISPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.mpris", "kdeconnect.mpris.request"}
}
func (p *MPRISPlugin) OutgoingTypes() []string { return []string{"kdeconnect.mpris"} }

type MPRISRequest struct {
	RequestPlayerList bool   `json:"requestPlayerList,omitempty"`
	RequestNowPlaying bool   `json:"requestNowPlaying,omitempty"`
	RequestVolume     bool   `json:"requestVolume,omitempty"`
	Player            string `json:"player,omitempty"`
	Action            string `json:"action,omitempty"`
	SetVolume         *int   `json:"setVolume,omitempty"`
	Seek              *int64 `json:"Seek,omitempty"`
	SetPosition       *int64 `json:"SetPosition,omitempty"`
	SetShuffle        *bool  `json:"setShuffle,omitempty"`
	SetLoopStatus     string `json:"setLoopStatus,omitempty"`
	AlbumArtUrl       string `json:"albumArtUrl,omitempty"`
}

type NowPlaying struct {
	Player         string `json:"player"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	Album          string `json:"album"`
	AlbumArtUrl    string `json:"albumArtUrl"`
	Url            string `json:"url,omitempty"`
	Length         int64  `json:"length"`
	Pos            int64  `json:"pos,omitempty"`
	IsPlaying      bool   `json:"isPlaying"`
	Volume         int    `json:"volume,omitempty"`
	CanControl     bool   `json:"canControl"`
	CanGoNext      bool   `json:"canGoNext"`
	CanGoPrevious  bool   `json:"canGoPrevious"`
	CanPause       bool   `json:"canPause"`
	CanPlay        bool   `json:"canPlay"`
	CanSeek        bool   `json:"canSeek"`
	PlaybackStatus string `json:"playbackStatus"`
	Shuffle        *bool  `json:"shuffle,omitempty"`
	LoopStatus     string `json:"loopStatus,omitempty"`
}

func (p *MPRISPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	p.mu.Lock()
	if _, exists := p.devices[dev.ID()]; !exists {
		p.devices[dev.ID()] = dev
	}
	p.mu.Unlock()

	var body MPRISRequest
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	if body.AlbumArtUrl != "" && strings.HasPrefix(body.AlbumArtUrl, "file://") {
		go p.sendAlbumArt(ctx, dev, body.Player, body.AlbumArtUrl)
		return nil
	}

	if body.RequestPlayerList {
		return p.sendPlayerList(dev)
	}

	if body.Player == "" {
		return nil
	}

	if body.RequestNowPlaying || body.RequestVolume {
		go func() {
			if state, err := p.playerState(body.Player); err == nil {
				p.broadcast(state)
			}
		}()
		return nil
	}

	if body.Action != "" || body.Seek != nil || body.SetPosition != nil || body.SetVolume != nil || body.SetShuffle != nil || body.SetLoopStatus != "" {
		go p.handleAction(body.Player, body.Action, body.Seek, body.SetPosition, body.SetVolume, body.SetShuffle, body.SetLoopStatus)
	}

	return nil
}

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

	p.logger.Debug("mpris: sending player list", zap.Strings("players", displayNames))

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
	pkt, err := protocol.NewPacket("kdeconnect.mpris", state)
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

	p.logger.Debug("mpris: added player", zap.String("displayName", displayName), zap.String("busName", busName))

	if state, err := p.playerState(displayName); err == nil {
		p.mu.Lock()
		p.lastStates[displayName] = state
		p.mu.Unlock()
		p.broadcast(state)
	}

	p.sendPlayerListBroadcast()
}

func (p *MPRISPlugin) removePlayer(displayName string) {
	p.mu.Lock()
	delete(p.players, displayName)
	delete(p.lastTracks, displayName)
	delete(p.lastStates, displayName)
	p.mu.Unlock()

	p.logger.Debug("mpris: removed player", zap.String("displayName", displayName))

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

func (p *MPRISPlugin) sendAlbumArt(ctx context.Context, dev device.Sender, player, artUrl string) {
	p.mu.Lock()
	reqKey := dev.ID() + "|" + artUrl
	if lastReq, exists := p.artRequests[reqKey]; exists && time.Since(lastReq) < 5*time.Second {
		p.mu.Unlock()
		return
	}
	p.artRequests[reqKey] = time.Now()
	p.mu.Unlock()

	cleanUrl := artUrl
	if idx := strings.LastIndex(cleanUrl, "?t="); idx != -1 {
		cleanUrl = cleanUrl[:idx]
	}

	filePath := strings.TrimPrefix(cleanUrl, "file://")
	if unescaped, err := url.PathUnescape(filePath); err == nil {
		filePath = unescaped
	}

	f, err := os.Open(filePath)
	if err != nil {
		p.logger.Debug("mpris: album art file not found", zap.String("path", filePath), zap.Error(err))
		return
	}
	stat, err := f.Stat()
	f.Close()
	if err != nil {
		return
	}

	var shareCfg config.ShareConfig
	shareCfg.Defaults()

	ln, port, err := share.ListenSideChannel(ctx, shareCfg, p.tlsConfig)
	if err != nil {
		return
	}

	go func() {
		_ = share.AcceptAndSend(ln, filePath, p.tlsConfig, dev.ID(), 10*time.Second, nil, p.logger)
	}()

	pkt, err := protocol.NewPacket("kdeconnect.mpris", map[string]interface{}{
		"transferringAlbumArt": true,
		"player":               player,
		"albumArtUrl":          artUrl,
	})
	if err == nil {
		pkt.PayloadSize = stat.Size()
		pkt.PayloadTransferInfo = &protocol.TransferInfo{Port: port}
		dev.Send(pkt)
	}
}

func (p *MPRISPlugin) OnConnect(dev device.Sender) {}

func (p *MPRISPlugin) OnDisconnect(dev device.Sender) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.devices, dev.ID())
}

type DebugPlayerInfo struct {
	DisplayName    string `json:"displayName"`
	BusName        string `json:"busName"`
	ShortName      string `json:"shortName"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	Album          string `json:"album"`
	PlaybackStatus string `json:"playbackStatus"`
	IsPlaying      bool   `json:"isPlaying"`
	Volume         int    `json:"volume"`
	Pos            int64  `json:"pos"`
	Length         int64  `json:"length"`
	AlbumArtUrl    string `json:"albumArtUrl"`
	CanSeek        bool   `json:"canSeek"`
	CanGoNext      bool   `json:"canGoNext"`
	CanGoPrevious  bool   `json:"canGoPrevious"`
	CanPlay        bool   `json:"canPlay"`
	CanPause       bool   `json:"canPause"`
	Error          string `json:"error,omitempty"`
}

type DebugStatus struct {
	WatcherRunning bool              `json:"watcherRunning"`
	DeviceCount    int               `json:"deviceCount"`
	Players        []DebugPlayerInfo `json:"players"`
	PlayerMappings map[string]string `json:"playerMappings"`
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
