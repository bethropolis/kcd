package mpris

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/godbus/dbus/v5"
	"go.uber.org/zap"
)

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

	go func() {
		for {
			if ctx.Err() != nil {
				return
			}
			if err := p.runPositionWatcher(ctx); err != nil && ctx.Err() == nil {
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

		np, err := p.parseOutput(playerName, line)
		if err != nil {
			p.logger.Debug("mpris: parse error", zap.Error(err))
			continue
		}

		p.broadcast(np)
	}

	_ = cmd.Wait()
	return scanner.Err()
}

func (p *MPRISPlugin) runPositionWatcher(ctx context.Context) error {
	// playerctl --all-players --follow --format "{{playerName}}|||{{position}}"
	cmd := plugin.NewPlayerctlCmd(ctx, "--all-players", "--follow", "--format", "{{playerName}}|||{{position}}")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("playerctl --follow position: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Split(line, "|||")
		if len(parts) < 2 {
			continue
		}

		playerName := parts[0]
		pos, _ := strconv.ParseInt(parts[1], 10, 64)

		pkt, _ := protocol.NewPacket("kdeconnect.mpris", map[string]interface{}{
			"player": playerName,
			"pos":    pos / 1000,
		})

		p.mu.RLock()
		for _, dev := range p.devices {
			if dev.IsConnected() {
				_ = dev.Send(pkt)
			}
		}
		p.mu.RUnlock()
	}

	_ = cmd.Wait()
	return scanner.Err()
}

// watchPlayerListDBus uses D-Bus signals to provide instant player list updates.
func (p *MPRISPlugin) watchPlayerListDBus(ctx context.Context) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		p.logger.Warn("mpris: D-Bus session bus unavailable, falling back to poll", zap.Error(err))
		p.watchPlayerList(ctx)
		return
	}
	defer conn.Close()

	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
	); err != nil {
		p.logger.Warn("mpris: failed to add D-Bus match signal, falling back to poll", zap.Error(err))
		p.watchPlayerList(ctx)
		return
	}

	ch := make(chan *dbus.Signal, 16)
	conn.Signal(ch)

	// Initial broadcast
	p.broadcastPlayerList()

	for {
		select {
		case <-ctx.Done():
			return
		case sig := <-ch:
			if len(sig.Body) < 1 {
				continue
			}
			name, _ := sig.Body[0].(string)
			if !strings.HasPrefix(name, "org.mpris.MediaPlayer2.") {
				continue
			}
			// Skip playerctld as it's just a proxy
			if name == "org.mpris.MediaPlayer2.playerctld" {
				continue
			}

			p.broadcastPlayerList()
		}
	}
}

func (p *MPRISPlugin) broadcastPlayerList() {
	players, _ := listPlayers()
	if players == nil {
		players = []string{}
	}
	pkt, _ := protocol.NewPacket("kdeconnect.mpris", map[string]interface{}{
		"playerList":             players,
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

// watchPlayerList polls for active players as a fallback.
func (p *MPRISPlugin) watchPlayerList(ctx context.Context) {
	var lastPlayers string
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			players, err := listPlayers()
			if err != nil {
				continue
			}
			current := strings.Join(players, ",")
			if current != lastPlayers {
				lastPlayers = current
				p.broadcastPlayerList()
			}
		}
	}
}

