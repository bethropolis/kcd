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

func (p *MPRISPlugin) runDBusWatcher(ctx context.Context) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()

	// Map from unique D-Bus name (e.g. ":1.42") to display name, local to this watcher.
	uniqueToDisplay := make(map[string]string)

	// Enumerate existing MPRIS players
	entries, _ := listPlayersDBus(p.dbus, p.logger)
	for _, e := range entries {
		var owner string
		if err := conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, e.busName).Store(&owner); err == nil {
			uniqueToDisplay[owner] = e.identity
			_ = conn.AddMatchSignal(
				dbus.WithMatchSender(e.busName),
				dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
				dbus.WithMatchMember("PropertiesChanged"),
			)
			_ = conn.AddMatchSignal(
				dbus.WithMatchSender(e.busName),
				dbus.WithMatchInterface("org.mpris.MediaPlayer2.Player"),
				dbus.WithMatchMember("Seeked"),
			)
			p.addPlayer(e.busName, owner, e.identity, e.shortName)
		}
	}

	_ = conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
	)

	ch := make(chan *dbus.Signal, 64)
	conn.Signal(ch)

	for {
		select {
		case <-ctx.Done():
			return nil
		case sig := <-ch:
			if sig == nil {
				continue
			}

			switch sig.Name {
			case "NameOwnerChanged":
				p.handleNameOwnerChanged(sig, conn, uniqueToDisplay)
			case "Seeked":
				p.handleSeeked(sig, uniqueToDisplay)
			case "PropertiesChanged":
				p.handlePropertiesChanged(sig, uniqueToDisplay)
			}
		}
	}
}

func (p *MPRISPlugin) handleNameOwnerChanged(sig *dbus.Signal, conn *dbus.Conn, uniqueToDisplay map[string]string) {
	if len(sig.Body) < 3 {
		return
	}
	name, _ := sig.Body[0].(string)
	oldOwner, _ := sig.Body[1].(string)
	newOwner, _ := sig.Body[2].(string)

	if !strings.HasPrefix(name, "org.mpris.MediaPlayer2.") ||
		strings.HasPrefix(name, "org.mpris.MediaPlayer2.kdeconnect.") ||
		name == "org.mpris.MediaPlayer2.playerctld" {
		return
	}

	if oldOwner != "" {
		if displayName, ok := uniqueToDisplay[oldOwner]; ok {
			p.removePlayer(displayName)
		}
		delete(uniqueToDisplay, oldOwner)
	}

	if newOwner != "" {
		uniqueToDisplay[newOwner] = name

		// Resolve identity
		entry := resolveIdentity(p.dbus, name)
		if entry == nil {
			return
		}

		_ = conn.AddMatchSignal(
			dbus.WithMatchSender(name),
			dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
			dbus.WithMatchMember("PropertiesChanged"),
		)
		_ = conn.AddMatchSignal(
			dbus.WithMatchSender(name),
			dbus.WithMatchInterface("org.mpris.MediaPlayer2.Player"),
			dbus.WithMatchMember("Seeked"),
		)

		uniqueToDisplay[newOwner] = entry.identity
		p.addPlayer(entry.busName, newOwner, entry.identity, entry.shortName)
	}
}

func resolveIdentity(conn *dbus.Conn, busName string) *playerEntry {
	short := strings.TrimPrefix(busName, "org.mpris.MediaPlayer2.")
	if idx := strings.Index(short, ".instance"); idx != -1 {
		short = short[:idx]
	}

	obj := conn.Object(busName, "/org/mpris/MediaPlayer2")
	var identity string
	if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2", "Identity").Store(&identity); err == nil && identity != "" {
		return &playerEntry{busName: busName, shortName: short, identity: identity}
	}
	return &playerEntry{busName: busName, shortName: short, identity: short}
}

func (p *MPRISPlugin) handleSeeked(sig *dbus.Signal, uniqueToDisplay map[string]string) {
	if len(sig.Body) < 1 {
		return
	}
	displayName, ok := uniqueToDisplay[string(sig.Sender)]
	if !ok {
		return
	}
	pos, ok := sig.Body[0].(int64)
	if !ok {
		return
	}

	pkt, _ := protocol.NewPacket("kdeconnect.mpris", map[string]interface{}{
		"player": displayName,
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

func (p *MPRISPlugin) handlePropertiesChanged(sig *dbus.Signal, uniqueToDisplay map[string]string) {
	displayName, ok := uniqueToDisplay[string(sig.Sender)]
	if !ok {
		// Fallback: try the sender directly as display name for players
		// that might send signals with their well-known name.
		if pl := p.resolvePlayer(string(sig.Sender)); pl != nil {
			displayName = pl.displayName
		}
	}
	if displayName == "" {
		return
	}

	if len(sig.Body) < 2 {
		return
	}
	iface, _ := sig.Body[0].(string)
	if iface != "org.mpris.MediaPlayer2.Player" {
		return
	}
	changed, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		return
	}

	relevantKeys := []string{"Metadata", "PlaybackStatus", "Volume", "CanPlay", "CanPause", "CanGoNext", "CanGoPrevious", "CanSeek", "LoopStatus", "Shuffle"}
	shouldBroadcast := false
	for _, key := range relevantKeys {
		if _, exists := changed[key]; exists {
			shouldBroadcast = true
			break
		}
	}
	if !shouldBroadcast {
		return
	}

	pl := p.resolvePlayer(displayName)
	if pl == nil {
		return
	}

	// Get last known state as base
	p.mu.RLock()
	state := p.lastStates[displayName]
	if state == nil {
		state = &NowPlaying{Player: displayName, CanControl: true}
	} else {
		// Copy to avoid mutation races
		cp := *state
		state = &cp
	}
	p.mu.RUnlock()

	// Apply changed properties from signal data — no D-Bus call needed
	fillChangedProps(state, changed)

	if metaV, ok := changed["Metadata"]; ok {
		if meta, ok := metaV.Value().(map[string]dbus.Variant); ok {
			if artUrlV, ok := meta["mpris:artUrl"]; ok {
				rawArtUrl := artUrlV.Value().(string)
				p.mu.Lock()
				last, exists := p.lastTracks[displayName]
				trackChanged := !exists || last.rawArtUrl != rawArtUrl
				if trackChanged {
					p.lastTracks[displayName] = trackIdentity{
						rawArtUrl: rawArtUrl,
						timestamp: time.Now().UnixNano(),
					}
				}
				p.mu.Unlock()
				if strings.HasPrefix(rawArtUrl, "file://") && trackChanged {
					state.AlbumArtUrl = rawArtUrl + "?t=" + time.Now().Format("150405.000000000")
				}
			}
		}
	}

	// Query position and canSeek from D-Bus (like C++ does)
	pos, canSeek := p.queryPositionAndCanSeek(pl)
	state.Pos = pos
	state.CanSeek = canSeek

	// Save to cache
	p.mu.Lock()
	p.lastStates[displayName] = state
	p.mu.Unlock()

	p.broadcast(state)
}
