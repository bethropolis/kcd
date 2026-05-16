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

	// Match PropertiesChanged signals
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
	entries, _ := listPlayersDBus(p.dbus, p.logger)
	p.mu.Lock()
	p.playerNameToBus = make(map[string]string)
	for _, e := range entries {
		p.playerNameToBus[e.identity] = e.busName
	}
	p.mu.Unlock()

	for _, e := range entries {
		if state, err := p.playerStateDBus(e.identity); err == nil {
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

			// Extract player bus name from the sender
			sender := string(sig.Sender)
			if !strings.HasPrefix(sender, "org.mpris.MediaPlayer2.") {
				continue
			}
			// Skip our own kdeconnect players
			if strings.HasPrefix(sender, "org.mpris.MediaPlayer2.kdeconnect.") {
				continue
			}

			// Map bus name to display name
			displayName := p.busNameToDisplayName(sender)

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
					if state, err := p.playerStateDBus(displayName); err == nil {
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
					"player": displayName,
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

// busNameToDisplayName converts a D-Bus bus name to the display name.
func (p *MPRISPlugin) busNameToDisplayName(busName string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	// First try exact match
	for displayName, bus := range p.playerNameToBus {
		if bus == busName {
			return displayName
		}
	}
	// Fallback: use short name
	short := strings.TrimPrefix(busName, "org.mpris.MediaPlayer2.")
	if idx := strings.Index(short, ".instance"); idx != -1 {
		short = short[:idx]
	}
	return short
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
			// Skip our own kdeconnect players
			if strings.HasPrefix(name, "org.mpris.MediaPlayer2.kdeconnect.") {
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
	entries, _ := listPlayersDBus(p.dbus, p.logger)
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
			entries, err := listPlayersDBus(p.dbus, p.logger)
			if err != nil {
				continue
			}
			var names []string
			for _, e := range entries {
				names = append(names, e.identity)
			}
			current := strings.Join(names, ",")
			if current != lastPlayers {
				lastPlayers = current
				p.broadcastPlayerList()
			}
		}
	}
}
