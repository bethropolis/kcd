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
	"github.com/bethropolis/kcd/internal/events"
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
	bus       *events.Bus
	mu        sync.RWMutex
	devices   map[string]device.Sender
	dbus      *dbus.Conn

	watchCancel     context.CancelFunc
	watching        bool
	telephonyCancel context.CancelFunc

	players          map[string]*trackedPlayer
	lastTracks       map[string]trackIdentity
	lastStates       map[string]*NowPlaying
	artRequests      map[string]time.Time
	remoteStates     map[string]*NowPlaying            // deviceID → last known state
	positionTrackers map[string]*remotePositionTracker // deviceID → position extrapolation

	pauseMusic        bool
	callPausedPlayers []string // names of local players paused during a call
}

type trackIdentity struct {
	rawArtUrl string
}

type remotePositionTracker struct {
	lastPosition   int64
	lastPositionAt time.Time
	playing        bool
}

func NewMPRISPlugin(tlsConfig *tls.Config, bus *events.Bus, pauseMusic bool, logger *zap.Logger) *MPRISPlugin {
	dbusConn, err := dbus.ConnectSessionBus()
	if err != nil {
		logger.Warn("mpris: failed to connect to D-Bus session bus", zap.Error(err))
	} else {
		logger.Info("mpris: connected to D-Bus session bus")
	}

	p := &MPRISPlugin{
		tlsConfig:         tlsConfig,
		logger:            logger.With(zap.String("plugin", "mpris")),
		bus:               bus,
		dbus:              dbusConn,
		pauseMusic:        pauseMusic,
		devices:           make(map[string]device.Sender),
		players:           make(map[string]*trackedPlayer),
		lastTracks:        make(map[string]trackIdentity),
		lastStates:        make(map[string]*NowPlaying),
		artRequests:       make(map[string]time.Time),
		remoteStates:      make(map[string]*NowPlaying),
		positionTrackers:  make(map[string]*remotePositionTracker),
		callPausedPlayers: make([]string, 0),
	}

	// Start the watcher immediately (like C++ does in constructor).
	// Devices are registered lazily as packets arrive.
	watchCtx, cancel := context.WithCancel(context.Background())
	p.watchCancel = cancel
	p.watching = true
	p.startWatcher(watchCtx)

	// Subscribe to telephony events for pause-music-on-call.
	if p.pauseMusic && p.dbus != nil {
		telephonyCtx, telephonyCancel := context.WithCancel(context.Background())
		p.telephonyCancel = telephonyCancel
		go p.watchTelephony(telephonyCtx)
	}

	return p
}

func (p *MPRISPlugin) Name() string           { return "MPRIS" }
func (p *MPRISPlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *MPRISPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.mpris", "kdeconnect.mpris.request"}
}
func (p *MPRISPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.mpris", "kdeconnect.mpris.request"}
}

type MPRISRequest struct {
	// Request fields
	RequestPlayerList bool `json:"requestPlayerList,omitempty"`
	RequestNowPlaying bool `json:"requestNowPlaying,omitempty"`
	RequestVolume     bool `json:"requestVolume,omitempty"`

	// Action fields
	Player        string `json:"player,omitempty"`
	Action        string `json:"action,omitempty"`
	SetVolume     *int   `json:"setVolume,omitempty"`
	Seek          *int64 `json:"Seek,omitempty"`
	SetPosition   *int64 `json:"SetPosition,omitempty"`
	SetShuffle    *bool  `json:"setShuffle,omitempty"`
	SetLoopStatus string `json:"setLoopStatus,omitempty"`

	// Album art
	AlbumArtUrl string `json:"albumArtUrl,omitempty"`

	// Player list from remote device
	PlayerList []string `json:"playerList,omitempty"`

	// NowPlaying fields — populated when phone sends state update
	Title          string `json:"title,omitempty"`
	Artist         string `json:"artist,omitempty"`
	Album          string `json:"album,omitempty"`
	Url            string `json:"url,omitempty"`
	Length         int64  `json:"length,omitempty"`
	Pos            int64  `json:"pos,omitempty"`
	IsPlaying      bool   `json:"isPlaying,omitempty"`
	Volume         int    `json:"volume,omitempty"`
	CanControl     bool   `json:"canControl,omitempty"`
	CanGoNext      bool   `json:"canGoNext,omitempty"`
	CanGoPrevious  bool   `json:"canGoPrevious,omitempty"`
	CanPause       bool   `json:"canPause,omitempty"`
	CanPlay        bool   `json:"canPlay,omitempty"`
	CanSeek        bool   `json:"canSeek,omitempty"`
	PlaybackStatus string `json:"playbackStatus,omitempty"`
	Shuffle        *bool  `json:"shuffle,omitempty"`
	LoopStatus     string `json:"loopStatus,omitempty"`
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

	// Incoming playerList from remote device — request status for each player
	if len(body.PlayerList) > 0 {
		p.logger.Debug("mpris: received player list from remote", zap.Strings("players", body.PlayerList))
		for _, player := range body.PlayerList {
			player := player
			go p.requestPlayerStatus(dev, player)
		}
		return nil
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
		return nil
	}

	// Incoming NowPlaying state update from a remote device
	if body.Title != "" || body.Artist != "" || body.Album != "" || body.IsPlaying || body.PlaybackStatus != "" {
		state := &NowPlaying{
			Player:         body.Player,
			Title:          body.Title,
			Artist:         body.Artist,
			Album:          body.Album,
			AlbumArtUrl:    body.AlbumArtUrl,
			Url:            body.Url,
			Length:         body.Length,
			Pos:            body.Pos,
			IsPlaying:      body.IsPlaying,
			Volume:         body.Volume,
			CanControl:     body.CanControl,
			CanGoNext:      body.CanGoNext,
			CanGoPrevious:  body.CanGoPrevious,
			CanPause:       body.CanPause,
			CanPlay:        body.CanPlay,
			CanSeek:        body.CanSeek,
			PlaybackStatus: body.PlaybackStatus,
			Shuffle:        body.Shuffle,
			LoopStatus:     body.LoopStatus,
		}
		p.mu.Lock()
		tracker, ok := p.positionTrackers[dev.ID()]
		if !ok {
			tracker = &remotePositionTracker{}
			p.positionTrackers[dev.ID()] = tracker
		}
		tracker.lastPosition = body.Pos
		tracker.lastPositionAt = time.Now()
		tracker.playing = body.IsPlaying
		shouldPublish := shouldPublishRemoteState(p.remoteStates[dev.ID()], state)
		p.remoteStates[dev.ID()] = state
		p.mu.Unlock()
		if shouldPublish && p.bus != nil {
			p.bus.Publish(events.TypeMprisUpdate, dev.ID(), state)
		}
		return nil
	}

	return nil
}

func shouldPublishRemoteState(last, current *NowPlaying) bool {
	if last == nil {
		return true
	}
	return last.Player != current.Player ||
		last.Title != current.Title ||
		last.Artist != current.Artist ||
		last.Album != current.Album ||
		last.PlaybackStatus != current.PlaybackStatus ||
		last.IsPlaying != current.IsPlaying ||
		last.Volume != current.Volume
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

func (p *MPRISPlugin) OnConnect(dev device.Sender) {
	p.logger.Info("mpris: device connected, requesting player list", zap.String("device_id", dev.ID()))
	go p.requestPlayerListPeriodic(dev)
}

func (p *MPRISPlugin) requestPlayerListPeriodic(dev device.Sender) {
	if !dev.IsConnected() {
		return
	}
	p.requestPlayerList(dev)
	timer := time.NewTimer(3 * time.Second)
	<-timer.C
	if !dev.IsConnected() {
		return
	}
	p.requestPlayerList(dev)
	timer.Reset(7 * time.Second)
	<-timer.C
	if !dev.IsConnected() {
		return
	}
	p.requestPlayerList(dev)
}

func (p *MPRISPlugin) OnDisconnect(dev device.Sender) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.devices, dev.ID())
	delete(p.remoteStates, dev.ID())
	delete(p.positionTrackers, dev.ID())
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

// SendAction sends a media control action to a remote device.
// Sends on both kdeconnect.mpris (for Android's old MprisPlugin) and
// kdeconnect.mpris.request (for MprisReceiverPlugin) to maximise compatibility.
func (p *MPRISPlugin) SendAction(dev device.Sender, player, action string, seek *int64, volume *int) error {
	body := MPRISRequest{
		Player:    player,
		Action:    action,
		SetVolume: volume,
		Seek:      seek,
	}
	pkt, err := protocol.NewPacket("kdeconnect.mpris.request", body)
	if err != nil {
		return err
	}
	if err := dev.Send(pkt); err != nil {
		return err
	}
	pkt2, err := protocol.NewPacket("kdeconnect.mpris", body)
	if err != nil {
		return err
	}
	return dev.Send(pkt2)
}

// requestPlayerList sends a request for the remote device's active player list.
func (p *MPRISPlugin) requestPlayerList(dev device.Sender) error {
	body := MPRISRequest{RequestPlayerList: true}
	pkt, err := protocol.NewPacket("kdeconnect.mpris.request", body)
	if err != nil {
		return err
	}
	if err := dev.Send(pkt); err != nil {
		return err
	}
	// Also send as kdeconnect.mpris for phone-side MprisReceiverPlugin/MprisPlugin.
	pkt2, err := protocol.NewPacket("kdeconnect.mpris", body)
	if err != nil {
		return err
	}
	return dev.Send(pkt2)
}

// requestPlayerStatus sends a requestNowPlaying + requestVolume for a specific player.
func (p *MPRISPlugin) requestPlayerStatus(dev device.Sender, player string) error {
	body := MPRISRequest{
		Player:            player,
		RequestNowPlaying: true,
		RequestVolume:     true,
	}
	pkt, err := protocol.NewPacket("kdeconnect.mpris.request", body)
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

// RequestState sends a requestNowPlaying to refresh remote state.
func (p *MPRISPlugin) RequestState(dev device.Sender, player string) error {
	if player == "" {
		return p.requestPlayerList(dev)
	}
	return p.requestPlayerStatus(dev, player)
}

// RemoteState returns the last known NowPlaying state for a remote device,
// with position extrapolated from the last update time if playing.
func (p *MPRISPlugin) RemoteState(deviceID string) *NowPlaying {
	p.mu.RLock()
	defer p.mu.RUnlock()
	state := p.remoteStates[deviceID]
	if state == nil {
		return nil
	}
	copy := *state
	if tracker, ok := p.positionTrackers[deviceID]; ok && tracker.playing {
		elapsed := time.Since(tracker.lastPositionAt).Milliseconds()
		copy.Pos = tracker.lastPosition + elapsed
	}
	return &copy
}

// RemoteStates returns all known remote device player states,
// with positions extrapolated from the last update time.
func (p *MPRISPlugin) RemoteStates() map[string]*NowPlaying {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make(map[string]*NowPlaying, len(p.remoteStates))
	for id, state := range p.remoteStates {
		if state == nil {
			continue
		}
		copy := *state
		if tracker, ok := p.positionTrackers[id]; ok && tracker.playing {
			elapsed := time.Since(tracker.lastPositionAt).Milliseconds()
			copy.Pos = tracker.lastPosition + elapsed
		}
		result[id] = &copy
	}
	return result
}

// watchTelephony subscribes to telephony events and pauses/resumes
// local MPRIS players when calls start/end.
func (p *MPRISPlugin) watchTelephony(ctx context.Context) {
	sub := p.bus.Subscribe(events.DefaultSubscriberCap,
		events.TypeTelephonyRinging,
		events.TypeTelephonyTalking,
		events.TypeTelephonyCanceled)
	defer sub.Close()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.C:
			p.handleTelephonyEvent(ev)
		}
	}
}

func (p *MPRISPlugin) handleTelephonyEvent(ev events.Event) {
	switch ev.Type {
	case events.TypeTelephonyRinging, events.TypeTelephonyTalking:
		p.pauseAllPlayers()
	case events.TypeTelephonyCanceled:
		p.resumePausedPlayers()
	}
}

// pauseAllPlayers pauses every currently-playing local MPRIS player.
// It only acts once per call — repeated ringing/talking events are no-ops
// while callPausedPlayers is non-empty.
func (p *MPRISPlugin) pauseAllPlayers() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.callPausedPlayers) > 0 {
		return // already paused for an active call
	}

	for name, pl := range p.players {
		state, err := p.playerStateDBus(pl.busName, name)
		if err != nil {
			continue
		}
		if !state.IsPlaying {
			continue
		}
		obj := p.dbus.Object(pl.busName, "/org/mpris/MediaPlayer2")
		if err := dbusCall(obj, "org.mpris.MediaPlayer2.Player.Pause").Err; err == nil {
			p.callPausedPlayers = append(p.callPausedPlayers, name)
		}
	}
}

// resumePausedPlayers resumes every player that was paused by pauseAllPlayers.
func (p *MPRISPlugin) resumePausedPlayers() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, name := range p.callPausedPlayers {
		pl := p.players[name]
		if pl == nil {
			continue
		}
		obj := p.dbus.Object(pl.busName, "/org/mpris/MediaPlayer2")
		_ = dbusCall(obj, "org.mpris.MediaPlayer2.Player.Play").Err
	}
	p.callPausedPlayers = p.callPausedPlayers[:0]
}

// ActivePlayers returns the list of player names from all remote device states.
func (p *MPRISPlugin) ActivePlayers() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	seen := make(map[string]struct{})
	for _, state := range p.remoteStates {
		if state != nil && state.Player != "" {
			seen[state.Player] = struct{}{}
		}
	}
	players := make([]string, 0, len(seen))
	for name := range seen {
		players = append(players, name)
	}
	return players
}
