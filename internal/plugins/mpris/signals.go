package mpris

import (
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/godbus/dbus/v5"
	"go.uber.org/zap"
)

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
		if pl := p.resolvePlayer(string(sig.Sender)); pl != nil {
			displayName = pl.displayName
		}
	}
	if displayName == "" {
		p.logger.Debug("mpris: signal dropped, unknown sender", zap.String("sender", string(sig.Sender)), zap.String("signal", sig.Name))
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

	p.mu.RLock()
	state := p.lastStates[displayName]
	if state == nil {
		state = &NowPlaying{Player: displayName, CanControl: true}
	} else {
		cp := *state
		state = &cp
	}
	p.mu.RUnlock()

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
					}
				}
				p.mu.Unlock()
				if strings.HasPrefix(rawArtUrl, "file://") && trackChanged {
					state.AlbumArtUrl = rawArtUrl + "?t=" + time.Now().Format("150405.000000000")
				}
			}
		}
	}

	pos, canSeek := p.queryPositionAndCanSeek(pl)
	state.Pos = pos
	state.CanSeek = canSeek

	p.mu.Lock()
	p.lastStates[displayName] = state
	p.mu.Unlock()

	p.broadcast(state)
}
