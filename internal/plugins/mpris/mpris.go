package mpris

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// outputFormat is the playerctl --format template.
// All fields are pipe-separated on one line; missing fields become empty strings.
const outputFormat = "{{playerName}}|{{status}}|{{title}}|{{artist}}|{{album}}|{{mpris:artUrl}}|{{mpris:length}}|{{position}}|{{volume}}|{{f_can_go_next}}|{{f_can_go_prev}}|{{f_can_pause}}|{{f_can_play}}|{{f_can_seek}}"

// MPRISPlugin controls desktop media players via playerctl.
// It does not use D-Bus directly — playerctl handles all bus wiring.
type MPRISPlugin struct {
	logger  *zap.Logger
	mu      sync.RWMutex
	devices map[string]device.Sender // connected phone devices

	watchCancel context.CancelFunc
	watching    bool
}

func NewMPRISPlugin(logger *zap.Logger) *MPRISPlugin {
	return &MPRISPlugin{
		logger:  logger.With(zap.String("plugin", "mpris")),
		devices: make(map[string]device.Sender),
	}
}

func (p *MPRISPlugin) Name() string            { return "MPRIS" }
func (p *MPRISPlugin) Timeout() time.Duration  { return 5 * time.Second }
func (p *MPRISPlugin) IncomingTypes() []string { return []string{"kdeconnect.mpris", "kdeconnect.mpris.request"} }
func (p *MPRISPlugin) OutgoingTypes() []string { return []string{"kdeconnect.mpris"} }

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

	if body.Player == "" {
		return nil
	}

	// Phone is asking for the current state of a player
	if body.Action == "" && body.Seek == nil && body.SetPosition == nil && body.Volume == nil {
		go func() {
			state, err := playerState(body.Player)
			if err != nil {
				p.logger.Debug("mpris: state read failed", zap.String("player", body.Player), zap.Error(err))
				return
			}
			p.broadcast(state)
		}()
		return nil
	}

	// Execute the action in a goroutine (Handle must not block)
	go p.handleAction(body.Player, body.Action, body.Seek, body.SetPosition, body.Volume)
	return nil
}

func (p *MPRISPlugin) handleAction(player, action string, seek, setPos *int64, volume *int) {
	var args []string

	switch action {
	case "Play", "Pause", "PlayPause", "Next", "Previous", "Stop":
		args = []string{"-p", player, strings.ToLower(action)}

	case "Seek":
		if seek == nil {
			return
		}
		// playerctl seek takes seconds (float); phone sends microseconds
		secs := float64(*seek) / 1_000_000.0
		args = []string{"-p", player, "seek", fmt.Sprintf("%+.6f", secs)}

	case "SetPosition":
		if setPos == nil {
			return
		}
		secs := float64(*setPos) / 1_000_000.0
		args = []string{"-p", player, "position", fmt.Sprintf("%.6f", secs)}

	default:
		if volume != nil {
			// volume is 0–100 from phone; playerctl takes 0.0–1.0
			args = []string{"-p", player, "volume", fmt.Sprintf("%.2f", float64(*volume)/100.0)}
		}
	}

	if len(args) == 0 {
		return
	}

	if err := exec.Command("playerctl", args...).Run(); err != nil {
		p.logger.Debug("mpris: action failed",
			zap.String("player", player),
			zap.String("action", action),
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
		"playerList":         players,
		"supportAlbumArtUrl": true,
	})
	if err != nil {
		return err
	}
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
	cmd := exec.CommandContext(ctx, "playerctl",
		"--all-players",
		"--follow",
		"metadata",
		"--format", outputFormat,
	)

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
		// The format starts with {{playerName}}
		parts := strings.Split(line, "|")
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
	out, err := exec.Command("playerctl",
		"-p", playerName,
		"metadata",
		"--format", outputFormat,
	).Output()
	if err != nil {
		return nil, fmt.Errorf("playerctl metadata: %w", err)
	}
	return parseOutput(playerName, strings.TrimSpace(string(out)))
}

func parseOutput(playerName, line string) (*NowPlaying, error) {
	parts := strings.Split(line, "|")
	if len(parts) < 9 {
		return nil, fmt.Errorf("unexpected playerctl output: %q", line)
	}

	// parts[6] = mpris:length in microseconds (playerctl returns µs)
	length, _ := strconv.ParseInt(parts[6], 10, 64)
	// parts[7] = position in microseconds
	pos, _ := strconv.ParseInt(parts[7], 10, 64)
	// parts[8] = volume as 0.0–1.0 float
	volF, _ := strconv.ParseFloat(parts[8], 64)

	np := &NowPlaying{
		Player:         parts[0],
		PlaybackStatus: parts[1],
		IsPlaying:      parts[1] == "Playing",
		Title:          parts[2],
		Artist:         parts[3],
		Album:          parts[4],
		AlbumArtUrl:    parts[5],
		Length:         length / 1000,   // µs → ms
		Pos:            pos / 1000,      // µs → ms
		Volume:         int(volF * 100), // 0.0–1.0 → 0–100
		CanControl:     true,
		CanGoNext:      parseBool(safeGet(parts, 9)),
		CanGoPrevious:  parseBool(safeGet(parts, 10)),
		CanPause:       parseBool(safeGet(parts, 11)),
		CanPlay:        parseBool(safeGet(parts, 12)),
		CanSeek:        parseBool(safeGet(parts, 13)),
	}
	return np, nil
}

func safeGet(parts []string, i int) string {
	if i < len(parts) {
		return parts[i]
	}
	return "true" // safe default
}

func parseBool(s string) bool {
	return s != "false" && s != "0" && s != ""
}

func listPlayers() ([]string, error) {
	out, err := exec.Command("playerctl", "--list-all").Output()
	if err != nil {
		// No players open — not an error
		return nil, nil
	}
	var players []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			players = append(players, line)
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
