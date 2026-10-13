package mpris

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// canonicalActions maps lowercase action names to the phone's spelling.
var canonicalActions = map[string]string{
	"play":      "Play",
	"pause":     "Pause",
	"playpause": "PlayPause",
	"next":      "Next",
	"previous":  "Previous",
	"stop":      "Stop",
}

// CanonicalAction returns the phone's spelling of action, or action if unknown.
func CanonicalAction(action string) string {
	if c, ok := canonicalActions[strings.ToLower(action)]; ok {
		return c
	}
	return action
}

// SendAction sends a control request on both mpris packet types (old and
// receiver plugins); relative seeks resolve to SetPosition or error out.
func (p *MPRISPlugin) SendAction(dev device.Sender, player, action string, seek, setPosition *int64, volume *int) error {
	if setPosition == nil && seek != nil {
		state := p.RemoteState(dev.ID())
		if state == nil {
			// Phones ignore Seek: without a tracked position there is nothing
			// to resolve, and sending it would report false success.
			return fmt.Errorf(
				"cannot apply a relative seek for %s: no playback position known yet (the phone has not reported one, or it was cleared by a disconnect); retry, or pass an absolute setPosition",
				player)
		}
		pos := max(state.Pos+*seek, 0)
		if state.Length > 0 {
			pos = min(pos, state.Length)
		}
		setPosition, seek = &pos, nil
	}
	if setPosition != nil {
		seek = nil // send only one
	}
	body := MPRISRequest{
		Player:      player,
		Action:      CanonicalAction(action),
		SetVolume:   volume,
		Seek:        seek,
		SetPosition: setPosition,
	}
	pkt, err := protocol.NewPacket("kdeconnect.mpris.request", body)
	if err != nil {
		return err
	}
	if err := dev.Send(pkt); err != nil {
		return err
	}
	pkt2, err := protocol.NewPacket("kdeconnect.mpris", body)
	if err != nil {
		return err
	}
	return dev.Send(pkt2)
}

// requestPlayerList sends a request for the remote device's active player list.
func (p *MPRISPlugin) requestPlayerList(dev device.Sender) error {
	body := MPRISRequest{RequestPlayerList: true}
	pkt, err := protocol.NewPacket("kdeconnect.mpris.request", body)
	if err != nil {
		return err
	}
	if err := dev.Send(pkt); err != nil {
		return err
	}
	// Also send as kdeconnect.mpris for phone-side MprisReceiverPlugin/MprisPlugin.
	pkt2, err := protocol.NewPacket("kdeconnect.mpris", body)
	if err != nil {
		return err
	}
	return dev.Send(pkt2)
}

// requestPlayerStatus sends a requestNowPlaying + requestVolume for a specific player.
func (p *MPRISPlugin) requestPlayerStatus(dev device.Sender, player string) error {
	body := MPRISRequest{
		Player:            player,
		RequestNowPlaying: true,
		RequestVolume:     true,
	}
	pkt, err := protocol.NewPacket("kdeconnect.mpris.request", body)
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

// RequestState sends a requestNowPlaying to refresh remote state.
func (p *MPRISPlugin) RequestState(dev device.Sender, player string) error {
	if player == "" {
		return p.requestPlayerList(dev)
	}
	return p.requestPlayerStatus(dev, player)
}

// markArtPending empties unloadable kdeconnect:/ art URIs and flags them, so
// serving paths agree with events: a URL or "" with ArtPending set.
func markArtPending(np *NowPlaying) {
	if np.AlbumArtUrl != "" && strings.HasPrefix(np.AlbumArtUrl, "kdeconnect:") {
		np.AlbumArtUrl = ""
		np.ArtPending = true
	}
}

// RemoteState returns the last known NowPlaying state for a remote device,
// with position extrapolated from the last update time if playing.
func (p *MPRISPlugin) RemoteState(deviceID string) *NowPlaying {
	p.mu.RLock()
	defer p.mu.RUnlock()
	state := p.remoteStates[deviceID]
	if state == nil {
		return nil
	}
	copy := state.DeepCopy()
	copy.AlbumArtUrl = p.resolveArtURL(copy.AlbumArtUrl)
	markArtPending(copy)
	if tracker, ok := p.positionTrackers[deviceID]; ok && tracker.playing {
		elapsed := time.Since(tracker.lastPositionAt).Milliseconds()
		copy.Pos = tracker.lastPosition + elapsed
	}
	return copy
}

// RemoteStateAge returns the time since the last remote state update for a device.
// Returns a large duration if no state has been received yet.
func (p *MPRISPlugin) RemoteStateAge(deviceID string) time.Duration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	t, ok := p.remoteStateTimes[deviceID]
	if !ok {
		return 365 * 24 * time.Hour // effectively "forever ago"
	}
	return time.Since(t)
}

// RemoteStates returns all known remote device player states,
// with positions extrapolated from the last update time.
func (p *MPRISPlugin) RemoteStates() map[string]*NowPlaying {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make(map[string]*NowPlaying, len(p.remoteStates))
	for id, state := range p.remoteStates {
		if state == nil {
			continue
		}
		copy := state.DeepCopy()
		copy.AlbumArtUrl = p.resolveArtURL(copy.AlbumArtUrl)
		markArtPending(copy)
		if tracker, ok := p.positionTrackers[id]; ok && tracker.playing {
			elapsed := time.Since(tracker.lastPositionAt).Milliseconds()
			copy.Pos = tracker.lastPosition + elapsed
		}
		result[id] = copy
	}
	return result
}

// resolveArtURL maps a cached kdeconnect:// album art URI to a loadable
// file:// URL. Non-kdeconnect URIs and not-yet-cached art pass through.
func (p *MPRISPlugin) resolveArtURL(raw string) string {
	if raw == "" || p.artCache == nil {
		return raw
	}
	if resolved := p.artCache.Resolve(raw); resolved != "" {
		return resolved
	}
	return raw
}

// ActivePlayers returns the list of player names from all remote device states.
func (p *MPRISPlugin) ActivePlayers() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	seen := make(map[string]struct{})
	for _, state := range p.remoteStates {
		if state != nil && state.Player != "" {
			seen[state.Player] = struct{}{}
		}
	}
	players := make([]string, 0, len(seen))
	for name := range seen {
		players = append(players, name)
	}
	return players
}

func (p *MPRISPlugin) OnConnect(dev device.Sender) {
	p.logger.Info("mpris: device connected, requesting player list", log.String("device_id", dev.ID()))
	p.requestReconcile()
	go p.requestPlayerListPeriodic(dev)
}

func (p *MPRISPlugin) requestPlayerListPeriodic(dev device.Sender) {
	if !dev.IsConnected() {
		return
	}
	p.requestPlayerList(dev)
	timer := time.NewTimer(3 * time.Second)
	<-timer.C
	if !dev.IsConnected() {
		return
	}
	p.requestPlayerList(dev)
	timer.Reset(7 * time.Second)
	<-timer.C
	if !dev.IsConnected() {
		return
	}
	p.requestPlayerList(dev)
}

// syncRemotePoller runs the remote-state poller only while mpris.update has
// an audience; pollRemoteStates keeps its own guard against racing unsubscribes.
func (p *MPRISPlugin) syncRemotePoller() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bus.HasSubscribers(events.TypeMprisUpdate) {
		if p.remotePollCancel == nil {
			ctx, cancel := context.WithCancel(p.watchCtx)
			p.remotePollCancel = cancel
			go p.runRemoteStatePoller(ctx)
		}
		return
	}
	if p.remotePollCancel != nil {
		p.remotePollCancel()
		p.remotePollCancel = nil
	}
}

// runRemoteStatePoller re-requests now-playing while watched; responses
// republish only on real change, catching clients that mount mid-track.
func (p *MPRISPlugin) runRemoteStatePoller(ctx context.Context) {
	ticker := time.NewTicker(remoteStatePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollRemoteStates()
		}
	}
}

// pollRemoteStates refreshes cached, actively-playing players; stopped ones
// go stale by design, and nothing is requested with no subscribers listening.
func (p *MPRISPlugin) pollRemoteStates() {
	if !p.bus.HasSubscribers(events.TypeMprisUpdate) {
		return
	}
	p.mu.RLock()
	type target struct {
		dev    device.Sender
		player string
	}
	var targets []target
	for id, dev := range p.devices {
		if !dev.IsConnected() {
			continue
		}
		state := p.remoteStates[id]
		if state == nil || state.Player == "" || !state.IsPlaying {
			continue
		}
		targets = append(targets, target{dev: dev, player: state.Player})
	}
	p.mu.RUnlock()

	for _, t := range targets {
		if err := p.requestPlayerStatus(t.dev, t.player); err != nil {
			p.logger.Debug("mpris: state poll request failed", log.Error(err))
		}
	}
}

func (p *MPRISPlugin) OnDisconnect(dev device.Sender) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.devices, dev.ID())
	delete(p.remoteStates, dev.ID())
	delete(p.remoteStateTimes, dev.ID())
	delete(p.positionTrackers, dev.ID())
}
