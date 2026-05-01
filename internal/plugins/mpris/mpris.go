package mpris

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/share"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// outputFormat uses a multi-character delimiter to avoid splitting on titles containing pipes.
const outputFormat = "{{playerName}}|||{{status}}|||{{title}}|||{{artist}}|||{{album}}|||{{mpris:artUrl}}|||{{mpris:length}}|||{{position}}|||{{volume}}"

// MPRISPlugin controls desktop media players via playerctl.
type MPRISPlugin struct {
	tlsConfig *tls.Config
	logger    *zap.Logger
	mu        sync.RWMutex
	devices   map[string]device.Sender // connected phone devices

	watchCancel context.CancelFunc
	watching    bool
}

func NewMPRISPlugin(tlsConfig *tls.Config, logger *zap.Logger) *MPRISPlugin {
	return &MPRISPlugin{
		tlsConfig: tlsConfig,
		logger:    logger.With(zap.String("plugin", "mpris")),
		devices:   make(map[string]device.Sender),
	}
}

func (p *MPRISPlugin) Name() string            { return "MPRIS" }
func (p *MPRISPlugin) Timeout() time.Duration  { return 5 * time.Second }
func (p *MPRISPlugin) IncomingTypes() []string { return []string{"kdeconnect.mpris", "kdeconnect.mpris.request"} }
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
	AlbumArtUrl       string `json:"albumArtUrl,omitempty"`  // Used for local art transfer requests
}

type NowPlaying struct {
	Player         string `json:"player"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	Album          string `json:"album"`
	AlbumArtUrl    string `json:"albumArtUrl"`
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
	Shuffle        *bool  `json:"shuffle,omitempty"`
	LoopStatus     string `json:"loopStatus,omitempty"`
}

// Handle processes incoming packets from the phone.
func (p *MPRISPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	// Check if playerctl is available
	if _, err := exec.LookPath("playerctl"); err != nil {
		p.logger.Warn("playerctl not found, MPRIS plugin disabled")
		return nil
	}

	// Register device on first packet
	p.mu.Lock()
	if _, exists := p.devices[dev.ID()]; !exists {
		p.devices[dev.ID()] = dev
		if !p.watching {
			p.watching = true
			watchCtx, cancel := context.WithCancel(context.Background())
			p.watchCancel = cancel
			p.startWatcher(watchCtx)
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
			if state, err := playerState(body.Player); err == nil {
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
	if state, err := playerState(player); err == nil {
		p.broadcast(state)
	}
}

func (p *MPRISPlugin) sendPlayerList(dev device.Sender) error {
	players, _ := listPlayers()
	if players == nil {
		players = []string{}
	}

	pkt, err := protocol.NewPacket("kdeconnect.mpris", map[string]interface{}{
		"playerList":             players,
		"supportAlbumArtPayload": true, // We now support the side-channel!
	})
	if err != nil {
		return err
	}

	// Broadast current states one by one
	go func() {
		for _, player := range players {
			if state, err := playerState(player); err == nil {
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
	filePath := strings.TrimPrefix(artUrl, "file://")
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
		// Use a 10s timeout for the image transfer
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

func (p *MPRISPlugin) startWatcher(ctx context.Context) {
	p.logger.Info("mpris: starting playerctl follow watcher")

	go func() {
		for {
			if ctx.Err() != nil {
				return
			}
			if err := p.runWatcher(ctx); err != nil && ctx.Err() == nil {
				p.logger.Warn("mpris: playerctl watcher exited, restarting in 3s", zap.Error(err))
				select {
				case <-time.After(3 * time.Second):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
}

func (p *MPRISPlugin) runWatcher(ctx context.Context) error {
	cmd := plugin.NewPlayerctlCmd(ctx, "--all-players", "--follow", "metadata", "--format", outputFormat)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("playerctl --follow: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "|||", 2)
		if len(parts) < 2 {
			continue
		}
		playerName := parts[0]

		np, err := parseOutput(playerName, line)
		if err != nil {
			p.logger.Debug("mpris: parse error", zap.Error(err))
			continue
		}

		p.broadcast(np)
	}

	_ = cmd.Wait()
	return scanner.Err()
}

func playerState(playerName string) (*NowPlaying, error) {
	out, err := plugin.NewPlayerctlCmd(nil, "-p", playerName, "metadata", "--format", outputFormat).Output()
	if err != nil {
		return nil, fmt.Errorf("playerctl metadata: %w", err)
	}
	np, err := parseOutput(playerName, strings.TrimSpace(string(out)))
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

func parseOutput(playerName, line string) (*NowPlaying, error) {
	// Clean up playerctl output
	line = strings.ReplaceAll(line, "<no value>", "")

	parts := strings.Split(line, "|||")
	if len(parts) < 9 {
		return nil, fmt.Errorf("unexpected playerctl output: %q", line)
	}

	length, _ := strconv.ParseInt(parts[6], 10, 64)
	pos, _ := strconv.ParseInt(parts[7], 10, 64)
	volF, _ := strconv.ParseFloat(parts[8], 64)

	np := &NowPlaying{
		Player:         parts[0],
		PlaybackStatus: parts[1],
		IsPlaying:      parts[1] == "Playing",
		Title:          parts[2],
		Artist:         parts[3],
		Album:          parts[4],
		AlbumArtUrl:    parts[5],
		Length:         length / 1000,
		Pos:            pos / 1000,
		Volume:         int(volF * 100),
		CanControl:     true,
		CanGoNext:      true,
		CanGoPrevious:  true,
		CanPause:       true,
		CanPlay:        true,
		CanSeek:        true,
	}

	if np.Title == "" {
		np.Title = "Unknown Media"
	}

	return np, nil
}

func listPlayers() ([]string, error) {
	// Use metadata to reliably get names instead of 'status' which breaks formatting
	out, err := plugin.NewPlayerctlCmd(nil, "-a", "metadata", "--format", "{{playerName}}").Output()
	if err != nil {
		return nil, nil
	}
	var players []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if !seen[line] {
				seen[line] = true
				players = append(players, line)
			}
		}
	}
	return players, nil
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
