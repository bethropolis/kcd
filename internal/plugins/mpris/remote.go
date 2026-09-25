package mpris

import (
	"context"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// SendAction sends a media control action to a remote device.
// Sends on both kdeconnect.mpris (for Android's old MprisPlugin) and
// kdeconnect.mpris.request (for MprisReceiverPlugin) to maximise compatibility.
func (p *MPRISPlugin) SendAction(dev device.Sender, player, action string, seek *int64, volume *int) error {
	body := MPRISRequest{
		Player:    player,
		Action:    action,
		SetVolume: volume,
		Seek:      seek,
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

// markArtPending empties unloadable kdeconnect:/ art URIs and flags them,
// so serving paths (status, snapshot, summaries) agree with published
// events: art is either a loadable URL or "" with ArtPending set.
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

// syncRemotePoller starts the remote-state poller when at least one
// subscriber listens for mpris.update and stops it when the audience
// drains. Invoked from the bus subscriber-change hook (which runs without
// the bus lock) and once at construction. The hook only manages the
// ticker lifecycle; pollRemoteStates keeps its own guard so a racing
// unsubscribe between ticks still sends nothing.
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

// runRemoteStatePoller periodically re-requests now-playing from every
// connected device that has a known active player, but only while somebody
// listens: the ticker itself exists only with mpris.update subscribers
// (see syncRemotePoller), and pollRemoteStates stays silent with zero
// subscribers even if a tick races an unsubscribe.
// The responses flow back through Handle, where shouldPublishRemoteState
// dedupes them, so an mpris.update is only republished when the state
// actually changes — not on every poll. This closes the "watch client
// misses mid-track state" gap from the initial dump's 10s freshness gate.
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

// pollRemoteStates requests a now-playing refresh from devices that have a
// cached, actively-playing player. Devices without a cached state (never
// reported a player) or whose player is stopped/paused are skipped — stopped
// players are intentionally left to go stale instead of keeping a ghost track
// perpetually fresh.
//
// The poller is demand-driven: with no subscriber for mpris.update (no
// `kcd watch` listening for media), answers would be consumed by nobody, so
// no requests go out. Subscribing re-arms the refresh within one interval.
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
