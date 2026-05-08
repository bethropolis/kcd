package mpris

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
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

// watchPlayerList polls for active players. By using playerctl -l, we reliably 
// detect players even if they are currently paused/stopped.
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

				if players == nil {
					players = []string{}
				}
				pkt, _ := protocol.NewPacket("kdeconnect.mpris", map[string]interface{}{
					"playerList":             players,
					"supportAlbumArtPayload": true,
				})

				p.mu.RLock()
				for _, dev := range p.devices {
					if dev.IsConnected() {
						_ = dev.Send(pkt)
					}
				}
				p.mu.RUnlock()
			}
		}
	}
}
