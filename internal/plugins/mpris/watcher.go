package mpris

import (
	"context"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/godbus/dbus/v5"
	"go.uber.org/zap"
)

func (p *MPRISPlugin) startWatcher(ctx context.Context) {
	if p.dbus == nil {
		p.logger.Warn("mpris: D-Bus not available, cannot start watcher")
		return
	}

	go func() {
		for {
			if ctx.Err() != nil {
				return
			}
			if err := p.runDBusWatcher(ctx); err != nil && ctx.Err() == nil {
				p.logger.Warn("mpris: D-Bus watcher exited, restarting in 3s", zap.Error(err))
				select {
				case <-time.After(3 * time.Second):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
}

// runDBusWatcher listens for PropertiesChanged signals from all MPRIS players.
func (p *MPRISPlugin) runDBusWatcher(ctx context.Context) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()

	// Match PropertiesChanged signals from org.mpris.MediaPlayer2.Player
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"),
	); err != nil {
		return err
	}

	// Also match Seeked signals
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface("org.mpris.MediaPlayer2.Player"),
		dbus.WithMatchMember("Seeked"),
	); err != nil {
		return err
	}

	ch := make(chan *dbus.Signal, 64)
	conn.Signal(ch)

	// Initial broadcast for all currently active players
	players, _ := listPlayers()
	for _, player := range players {
		if state, err := p.playerStateDBus(player); err == nil {
			p.broadcast(state)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case sig := <-ch:
			if sig == nil {
				continue
			}

			// Extract player name from the sender
			sender := string(sig.Sender)
			if !strings.HasPrefix(sender, "org.mpris.MediaPlayer2.") {
				continue
			}
			playerName := strings.TrimPrefix(sender, "org.mpris.MediaPlayer2.")
			if idx := strings.Index(playerName, ".instance"); idx != -1 {
				playerName = playerName[:idx]
			}

			if sig.Name == "org.freedesktop.DBus.Properties.PropertiesChanged" {
				// PropertiesChanged: (interface_name, changed_properties, invalidated_properties)
				if len(sig.Body) < 2 {
					continue
				}
				iface, _ := sig.Body[0].(string)
				if iface != "org.mpris.MediaPlayer2.Player" {
					continue
				}
				changed, ok := sig.Body[1].(map[string]dbus.Variant)
				if !ok {
					continue
				}

				// If any relevant property changed, fetch full state and broadcast
				relevantKeys := []string{"Metadata", "PlaybackStatus", "Volume", "Position", "CanPlay", "CanPause", "CanGoNext", "CanGoPrevious", "CanSeek"}
				shouldBroadcast := false
				for _, key := range relevantKeys {
					if _, exists := changed[key]; exists {
						shouldBroadcast = true
						break
					}
				}

				if shouldBroadcast {
					if state, err := p.playerStateDBus(playerName); err == nil {
						p.broadcast(state)
					}
				}
			} else if sig.Name == "org.mpris.MediaPlayer2.Player.Seeked" {
				// Seeked: (position in microseconds)
				if len(sig.Body) < 1 {
					continue
				}
				pos, ok := sig.Body[0].(int64)
				if !ok {
					continue
				}

				pkt, _ := protocol.NewPacket("kdeconnect.mpris", map[string]interface{}{
					"player": playerName,
					"pos":    pos / 1000, // microseconds to milliseconds
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

// watchPlayerListDBus uses D-Bus signals to provide instant player list updates.
func (p *MPRISPlugin) watchPlayerListDBus(ctx context.Context) {
	if p.dbus == nil {
		p.logger.Warn("mpris: D-Bus not available, falling back to poll")
		p.watchPlayerList(ctx)
		return
	}

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
