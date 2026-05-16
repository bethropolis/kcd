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

// MPRISPlugin controls desktop media players via D-Bus MPRIS interface.
type MPRISPlugin struct {
	tlsConfig *tls.Config
	logger    *zap.Logger
	mu        sync.RWMutex
	devices   map[string]device.Sender // connected phone devices
	dbus      *dbus.Conn

	watchCancel context.CancelFunc
	watching    bool

	// cache for tracking track changes to avoid over-requesting album art
	lastTracks map[string]trackIdentity

	// Debounce map to prevent rapid duplicate album art requests from Android
	artRequests map[string]time.Time

	// prevVolume tracks the last volume sent per player to avoid redundant broadcasts
	prevVolume map[string]float64

	// playerNameToBus maps display names (e.g. "VLC media player") → D-Bus bus names
	playerNameToBus map[string]string
}

type trackIdentity struct {
	title     string
	artist    string
	album     string
	rawArtUrl string
	timestamp int64
}

func NewMPRISPlugin(tlsConfig *tls.Config, logger *zap.Logger) *MPRISPlugin {
	dbusConn, err := dbus.ConnectSessionBus()
	if err != nil {
		logger.Warn("mpris: failed to connect to D-Bus session bus", zap.Error(err))
	} else {
		logger.Info("mpris: connected to D-Bus session bus")
	}

	return &MPRISPlugin{
		tlsConfig:       tlsConfig,
		logger:          logger.With(zap.String("plugin", "mpris")),
		dbus:            dbusConn,
		devices:         make(map[string]device.Sender),
		lastTracks:      make(map[string]trackIdentity),
		artRequests:     make(map[string]time.Time),
		prevVolume:      make(map[string]float64),
		playerNameToBus: make(map[string]string),
	}
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
	SetLoopStatus     string `json:"setLoopStatus,omitempty"` // "None", "Track", "Playlist"
	AlbumArtUrl       string `json:"albumArtUrl,omitempty"`   // Used for local art transfer requests
}

type NowPlaying struct {
	Player         string `json:"player"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	Album          string `json:"album"`
	AlbumArtUrl    string `json:"albumArtUrl"`
	Url            string `json:"url,omitempty"`
	Length         int64  `json:"length"`        // ms, -1 for unknown
	Pos            int64  `json:"pos,omitempty"` // ms
	IsPlaying      bool   `json:"isPlaying"`
	Volume         int    `json:"volume,omitempty"` // 0-100
	CanControl     bool   `json:"canControl"`
	CanGoNext      bool   `json:"canGoNext"`
	CanGoPrevious  bool   `json:"canGoPrevious"`
	CanPause       bool   `json:"canPause"`
	CanPlay        bool   `json:"canPlay"`
	CanSeek        bool   `json:"canSeek"`
	PlaybackStatus string `json:"playbackStatus"` // "Playing", "Paused", "Stopped"
	Shuffle        *bool  `json:"shuffle,omitempty"`
	LoopStatus     string `json:"loopStatus,omitempty"`
}

// Handle processes incoming packets from the phone.
func (p *MPRISPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	// Register device on first packet
	p.mu.Lock()
	if _, exists := p.devices[dev.ID()]; !exists {
		p.devices[dev.ID()] = dev
		if !p.watching {
			p.watching = true
			watchCtx, cancel := context.WithCancel(context.Background())
			p.watchCancel = cancel
			p.startWatcher(watchCtx)
			go p.watchPlayerListDBus(watchCtx)
		}
	}
	p.mu.Unlock()

	var body MPRISRequest
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	// 1. Phone is asking to download a local album art file
	if body.AlbumArtUrl != "" && strings.HasPrefix(body.AlbumArtUrl, "file://") {
		go p.sendAlbumArt(ctx, dev, body.Player, body.AlbumArtUrl)
		return nil
	}

	// 2. Phone wants the list of players
	if body.RequestPlayerList {
		return p.sendPlayerList(dev)
	}

	if body.Player == "" {
		return nil
	}

	// 3. Phone explicitly requested current state to update its UI
	if body.RequestNowPlaying || body.RequestVolume {
		go func() {
			if state, err := p.playerState(body.Player); err == nil {
				p.broadcast(state)
			}
		}()
		return nil
	}

	// 4. Phone sent an action
	if body.Action != "" || body.Seek != nil || body.SetPosition != nil || body.SetVolume != nil || body.SetShuffle != nil || body.SetLoopStatus != "" {
		go p.handleAction(body.Player, body.Action, body.Seek, body.SetPosition, body.SetVolume, body.SetShuffle, body.SetLoopStatus)
	}

	return nil
}

func (p *MPRISPlugin) sendPlayerList(dev device.Sender) error {
	entries, _ := listPlayersDBus(p.dbus)
	var displayNames []string

	p.mu.Lock()
	p.playerNameToBus = make(map[string]string)
	for _, e := range entries {
		displayNames = append(displayNames, e.identity)
		p.playerNameToBus[e.identity] = e.busName
	}
	p.mu.Unlock()

	if displayNames == nil {
		displayNames = []string{}
	}

	pkt, err := protocol.NewPacket("kdeconnect.mpris", map[string]interface{}{
		"playerList":             displayNames,
		"supportAlbumArtPayload": true,
	})
	if err != nil {
		return err
	}

	// Broadcast current states one by one
	go func() {
		for _, name := range displayNames {
			if state, err := p.playerState(name); err == nil {
				p.broadcast(state)
			}
		}
	}()

	return dev.Send(pkt)
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

func (p *MPRISPlugin) sendAlbumArt(ctx context.Context, dev device.Sender, player, artUrl string) {
	// Debounce: prevent the Android app from spamming duplicate art requests
	// for the same track within a 5-second window.
	p.mu.Lock()
	reqKey := dev.ID() + "|" + artUrl
	if lastReq, exists := p.artRequests[reqKey]; exists && time.Since(lastReq) < 5*time.Second {
		p.mu.Unlock()
		return
	}
	p.artRequests[reqKey] = time.Now()
	p.mu.Unlock()

	// Validate: check that the requested albumArtUrl matches the player's current artUrl
	// (matching C++ behavior in mpriscontrolplugin.cpp:230-235)
	busName := p.resolvePlayerBus(player)
	if busName != "" {
		currentArtUrl := p.getPlayerArtUrl(busName)
		cleanRequested := artUrl
		if idx := strings.LastIndex(cleanRequested, "?t="); idx != -1 {
			cleanRequested = cleanRequested[:idx]
		}
		if currentArtUrl == "" || currentArtUrl != cleanRequested {
			p.logger.Debug("mpris: album art URL mismatch, ignoring request",
				zap.String("player", player),
				zap.String("requested", cleanRequested),
				zap.String("current", currentArtUrl),
			)
			return
		}
	}

	// Strip the cache-buster timestamp before opening the local file
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
		return
	}
	stat, err := f.Stat()
	f.Close()
	if err != nil {
		return
	}

	// Use default share config for the side-channel
	var shareCfg config.ShareConfig
	shareCfg.Defaults()

	// Open TLS side-channel
	ln, port, err := share.ListenSideChannel(ctx, shareCfg, p.tlsConfig)
	if err != nil {
		return
	}

	go func() {
		// 10-second timeout prevents port exhaustion if the phone ignores the art
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
	if len(p.devices) == 0 && p.watching {
		p.watching = false
		if p.watchCancel != nil {
			p.watchCancel()
			p.watchCancel = nil
		}
	}
}
