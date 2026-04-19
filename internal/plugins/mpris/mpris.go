package mpris

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/godbus/dbus/v5"
	"go.uber.org/zap"
)

const (
	mprisPrefix = "org.mpris.MediaPlayer2."
	mprisObject = "/org/mpris/MediaPlayer2"
	mprisIface  = "org.mpris.MediaPlayer2.Player"
)

// MPRISPlugin integrates with the desktop media players via D-Bus.
type MPRISPlugin struct {
	logger  *zap.Logger
	conn    *dbus.Conn
	mu      sync.RWMutex
	devices map[string]device.Sender

	activeSubscribers int
	stopFunc          context.CancelFunc
	signalChan        chan *dbus.Signal // Added to prevent DBus memory leaks

	// Cache to map unique D-Bus names (e.g. :1.45) to well-known names (e.g. org.mpris.MediaPlayer2.spotify)
	nameCacheMu sync.RWMutex
	nameCache   map[string]string
}

// NewMPRISPlugin creates a new MPRISPlugin and connects to the session bus.
func NewMPRISPlugin(logger *zap.Logger) (*MPRISPlugin, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("mpris: failed to connect to session bus: %w", err)
	}

	return &MPRISPlugin{
		logger:    logger.With(zap.String("plugin", "mpris")),
		conn:      conn,
		devices:   make(map[string]device.Sender),
		nameCache: make(map[string]string),
	}, nil
}

func (p *MPRISPlugin) Name() string { return "MPRIS" }

func (p *MPRISPlugin) Timeout() time.Duration { return 5 * time.Second }

func (p *MPRISPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.mpris", "kdeconnect.mpris.request"}
}

func (p *MPRISPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.mpris"}
}

type MPRISRequest struct {
	RequestPlayerList bool   `json:"requestPlayerList,omitempty"`
	Player            string `json:"player,omitempty"`
	Action            string `json:"action,omitempty"`
	Volume            *int   `json:"volume,omitempty"`
	Seek              *int64 `json:"Seek,omitempty"`
	SetPosition       *int64 `json:"SetPosition,omitempty"`
}

type NowPlaying struct {
	Player         string `json:"player"`
	Title          string `json:"title,omitempty"`
	Artist         string `json:"artist,omitempty"`
	Album          string `json:"album,omitempty"`
	AlbumArtUrl    string `json:"albumArtUrl,omitempty"`
	Length         int64  `json:"length,omitempty"` // ms
	Pos            int64  `json:"pos,omitempty"`    // ms
	IsPlaying      bool   `json:"isPlaying"`
	Volume         int    `json:"volume,omitempty"` // 0-100
	CanControl     bool   `json:"canControl"`
	CanGoNext      bool   `json:"canGoNext"`
	CanGoPrevious  bool   `json:"canGoPrevious"`
	CanPause       bool   `json:"canPause"`
	CanPlay        bool   `json:"canPlay"`
	CanSeek        bool   `json:"canSeek"`
	PlaybackStatus string `json:"playbackStatus"` // "Playing", "Paused", "Stopped"
}

// Handle processes incoming MPRIS control packets.
func (p *MPRISPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	p.mu.Lock()
	if _, exists := p.devices[dev.ID()]; !exists {
		p.devices[dev.ID()] = dev
		p.activeSubscribers++
		if p.activeSubscribers == 1 {
			// Create a background context for the D-Bus monitor loop
			bgCtx, cancel := context.WithCancel(context.Background())
			p.stopFunc = cancel
			p.Start(bgCtx)
		}
	}
	p.mu.Unlock()

	var body MPRISRequest
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	if body.RequestPlayerList {
		return p.sendPlayerList(dev)
	}

	if body.Player != "" {
		if body.Action != "" {
			p.handleAction(body.Player, body.Action, 0)
		} else if body.Seek != nil {
			p.handleAction(body.Player, "Seek", *body.Seek)
		} else if body.SetPosition != nil {
			p.handleAction(body.Player, "SetPosition", *body.SetPosition)
		} else if body.Volume != nil {
			p.handleAction(body.Player, "Volume", int64(*body.Volume))
		}
	}

	return nil
}

// Start initiates the background D-Bus signal monitoring.
func (p *MPRISPlugin) Start(ctx context.Context) {
	p.logger.Info("starting MPRIS D-Bus listener")
	// Subscribe to NameOwnerChanged to maintain sender cache
	p.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, "type='signal',interface='org.freedesktop.DBus',member='NameOwnerChanged'")

	// Subscribe to PropertiesChanged signals for all players
	rule := "type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',path='/org/mpris/MediaPlayer2'"
	p.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule)

	p.signalChan = make(chan *dbus.Signal, 10)
	p.conn.Signal(p.signalChan)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case sig := <-p.signalChan:
				if sig == nil {
					return
				}
				if sig.Name == "org.freedesktop.DBus.NameOwnerChanged" && len(sig.Body) == 3 {
					name := sig.Body[0].(string)
					oldOwner := sig.Body[1].(string)
					newOwner := sig.Body[2].(string)

					p.nameCacheMu.Lock()
					if oldOwner != "" {
						delete(p.nameCache, oldOwner)
					}
					if newOwner != "" && strings.HasPrefix(name, mprisPrefix) {
						p.nameCache[newOwner] = name
					}
					p.nameCacheMu.Unlock()
				} else if sig.Name == "org.freedesktop.DBus.Properties.PropertiesChanged" && strings.HasPrefix(sig.Sender, ":") {
					p.onPropertiesChanged(sig)
				}
			}
		}
	}()
}

func (p *MPRISPlugin) onPropertiesChanged(sig *dbus.Signal) {
	if len(sig.Body) < 2 {
		return
	}
	if sig.Body[0].(string) != mprisIface {
		return
	}

	// Body[1] is the map of changed properties (unused for now, we fetch full state)

	// Determine which well-known name this sender has
	var name string
	p.conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, sig.Sender).Store(&name)
	// This is tricky because GetNameOwner returns the ID for a name, not vice versa.
	// We'll just list names and find the owner of sig.Sender.

	wellKnown := p.findWellKnownName(sig.Sender)
	if wellKnown == "" {
		return
	}

	// Fetch full state for this player to be sure
	state := p.getPlayerState(wellKnown)
	if state == nil {
		return
	}

	p.broadcast(state)
}

func (p *MPRISPlugin) findWellKnownName(sender string) string {
	p.nameCacheMu.RLock()
	if name, ok := p.nameCache[sender]; ok {
		p.nameCacheMu.RUnlock()
		return name
	}
	p.nameCacheMu.RUnlock()

	var names []string
	p.conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names)
	for _, n := range names {
		if !strings.HasPrefix(n, mprisPrefix) {
			continue
		}
		var owner string
		p.conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, n).Store(&owner)

		p.nameCacheMu.Lock()
		p.nameCache[owner] = n
		p.nameCacheMu.Unlock()

		if owner == sender {
			return n
		}
	}
	return ""
}

// Safe extraction helpers to prevent panics from invalid D-Bus variant types

func variantString(v dbus.Variant) string {
	if v.Value() == nil {
		return ""
	}
	switch val := v.Value().(type) {
	case string:
		return val
	default:
		return ""
	}
}

func variantStringSlice(v dbus.Variant) []string {
	if v.Value() == nil {
		return nil
	}
	switch val := v.Value().(type) {
	case []string:
		return val
	default:
		return nil
	}
}

func variantInt64(v dbus.Variant) int64 {
	if v.Value() == nil {
		return 0
	}
	switch val := v.Value().(type) {
	case int64:
		return val
	case uint64:
		return int64(val)
	case int32:
		return int64(val)
	case uint32:
		return int64(val)
	case float64:
		return int64(val)
	default:
		return 0
	}
}

func (p *MPRISPlugin) getPlayerState(name string) *NowPlaying {
	obj := p.conn.Object(name, mprisObject)

	status, _ := obj.GetProperty(mprisIface + ".PlaybackStatus")
	metadata, _ := obj.GetProperty(mprisIface + ".Metadata")

	if status.Value() == nil || metadata.Value() == nil {
		return nil
	}

	m, ok := metadata.Value().(map[string]dbus.Variant)
	if !ok {
		return nil
	}

	np := &NowPlaying{
		Player:         strings.TrimPrefix(name, mprisPrefix),
		PlaybackStatus: variantString(status),
		IsPlaying:      variantString(status) == "Playing",
	}

	if val, ok := m["xesam:title"]; ok {
		np.Title = variantString(val)
	}
	if val, ok := m["xesam:artist"]; ok {
		artists := variantStringSlice(val)
		if len(artists) > 0 {
			np.Artist = strings.Join(artists, ", ")
		}
	}
	if val, ok := m["xesam:album"]; ok {
		np.Album = variantString(val)
	}
	if val, ok := m["mpris:artUrl"]; ok {
		np.AlbumArtUrl = variantString(val)
	}
	if val, ok := m["mpris:length"]; ok {
		np.Length = variantInt64(val) / 1000 // usec to msec
	}

	// Caps
	np.CanControl = true
	np.CanGoNext = true
	np.CanGoPrevious = true
	np.CanPause = true
	np.CanPlay = true
	np.CanSeek = true

	return np
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

func (p *MPRISPlugin) sendPlayerList(dev device.Sender) error {
	var names []string
	if err := p.conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return err
	}

	var players []string
	for _, name := range names {
		if strings.HasPrefix(name, mprisPrefix) {
			players = append(players, strings.TrimPrefix(name, mprisPrefix))
		}
	}

	pkt, err := protocol.NewPacket("kdeconnect.mpris", map[string]interface{}{
		"playerList": players,
	})
	if err != nil {
		return err
	}

	return dev.Send(pkt)
}

func (p *MPRISPlugin) handleAction(playerName string, action string, val int64) {
	// Re-add prefix for D-Bus
	busName := playerName
	if !strings.HasPrefix(busName, mprisPrefix) {
		busName = mprisPrefix + busName
	}

	obj := p.conn.Object(busName, mprisObject)

	// Handlers must not block per absolute rule.
	go func() {
		var call *dbus.Call
		switch action {
		case "Play":
			call = obj.Call(mprisIface+".Play", 0)
		case "Pause":
			call = obj.Call(mprisIface+".Pause", 0)
		case "PlayPause":
			call = obj.Call(mprisIface+".PlayPause", 0)
		case "Next":
			call = obj.Call(mprisIface+".Next", 0)
		case "Previous":
			call = obj.Call(mprisIface+".Previous", 0)
		case "Stop":
			call = obj.Call(mprisIface+".Stop", 0)
		case "Seek":
			call = obj.Call(mprisIface+".Seek", 0, val*1000)
		case "SetPosition":
			// SetPosition needs TrackId
			metadata, _ := obj.GetProperty(mprisIface + ".Metadata")
			if metadata.Value() != nil {
				m := metadata.Value().(map[string]dbus.Variant)
				if trackId, ok := m["mpris:trackid"]; ok {
					call = obj.Call(mprisIface+".SetPosition", 0, trackId.Value(), val*1000)
				}
			}
		}

		if call != nil && call.Err != nil {
			p.logger.Error("mpris action failed", zap.String("player", playerName), zap.String("action", action), zap.Error(call.Err))
		}
	}()
}

func (p *MPRISPlugin) OnConnect(dev device.Sender) {}

func (p *MPRISPlugin) OnDisconnect(dev device.Sender) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.devices[dev.ID()]; exists {
		delete(p.devices, dev.ID())
		p.activeSubscribers--
		if p.activeSubscribers == 0 {
			p.Stop()
		}
	}
}

// Stop terminates the background D-Bus signal monitoring.
func (p *MPRISPlugin) Stop() {
	if p.stopFunc != nil {
		p.logger.Info("stopping MPRIS D-Bus listener")
		p.stopFunc()
		p.stopFunc = nil

		// Crucial: Tell godbus to stop sending to this channel to prevent blocking the D-Bus router
		if p.signalChan != nil {
			p.conn.RemoveSignal(p.signalChan)
			p.signalChan = nil
		}

		// Unsubscribe from signals
		p.conn.BusObject().Call("org.freedesktop.DBus.RemoveMatch", 0, "type='signal',interface='org.freedesktop.DBus',member='NameOwnerChanged'")
		rule := "type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',path='/org/mpris/MediaPlayer2'"
		p.conn.BusObject().Call("org.freedesktop.DBus.RemoveMatch", 0, rule)
	}
}
