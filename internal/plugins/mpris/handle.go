package mpris

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

func (p *MPRISPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	p.mu.Lock()
	if _, exists := p.devices[dev.ID()]; !exists {
		p.devices[dev.ID()] = dev
	}
	p.mu.Unlock()

	var body MPRISRequest
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	if body.AlbumArtUrl != "" && strings.HasPrefix(body.AlbumArtUrl, "file://") {
		go p.sendAlbumArt(ctx, dev, body.Player, body.AlbumArtUrl)
		return nil
	}

	// Inbound album art payload — the phone responds to requestAlbumArt
	// with a side-channel transfer carrying the art bytes.
	if body.TransferringAlbumArt && pkt.PayloadSize > 0 && pkt.PayloadTransferInfo != nil {
		go p.receiveAlbumArt(ctx, dev, body.Player, body.AlbumArtUrl,
			pkt.PayloadSize, pkt.PayloadTransferInfo.Port)
		return nil
	}

	if body.RequestPlayerList {
		// The phone asks for current truth: heal any signal drift first
		// (async — the answer below goes out from the current map and the
		// reconcile follow-up broadcasts corrections). Handle stays
		// non-blocking per the plugin contract.
		p.requestReconcile()
		return p.sendPlayerList(dev)
	}

	// Incoming playerList from remote device — prune players that no longer
	// exist (their media session was destroyed) and request fresh status for
	// the ones still around.
	if body.PlayerList != nil {
		p.logger.Debug("mpris: received player list from remote", log.Strings("players", body.PlayerList))
		pruned := false
		p.mu.Lock()
		if prev := p.remoteStates[dev.ID()]; prev != nil {
			inList := false
			for _, name := range body.PlayerList {
				if name == prev.Player {
					inList = true
					break
				}
			}
			if !inList {
				delete(p.remoteStates, dev.ID())
				delete(p.remoteStateTimes, dev.ID())
				delete(p.positionTrackers, dev.ID())
				pruned = true
			}
		}
		p.mu.Unlock()

		if pruned && p.bus != nil {
			// The tracked player's session is gone — emit an empty update so
			// watchers fall back to "no media playing" for this device.
			p.bus.Publish(events.TypeMprisUpdate, dev.ID(), &NowPlaying{})
		}

		for _, player := range body.PlayerList {
			player := player
			go p.requestPlayerStatus(dev, player)
		}
		return nil
	}

	if body.Player == "" {
		return nil
	}

	if body.RequestNowPlaying || body.RequestVolume {
		go func() {
			if state, err := p.playerState(body.Player); err == nil {
				p.broadcast(state)
			}
		}()
		return nil
	}

	if body.Action != "" || body.Seek != nil || body.SetPosition != nil || body.SetVolume != nil || body.SetShuffle != nil || body.SetLoopStatus != "" {
		go p.handleAction(body.Player, body.Action, body.Seek, body.SetPosition, body.SetVolume, body.SetShuffle, body.SetLoopStatus)
		return nil
	}

	// Incoming NowPlaying state update from a remote device
	if body.Title != "" || body.Artist != "" || body.Album != "" || body.IsPlaying || body.PlaybackStatus != "" {
		state := &NowPlaying{
			Player:         body.Player,
			Title:          body.Title,
			Artist:         body.Artist,
			Album:          body.Album,
			AlbumArtUrl:    body.AlbumArtUrl,
			Url:            body.Url,
			Length:         body.Length,
			Pos:            body.Pos,
			IsPlaying:      body.IsPlaying,
			Volume:         body.Volume,
			CanControl:     body.CanControl,
			CanGoNext:      body.CanGoNext,
			CanGoPrevious:  body.CanGoPrevious,
			CanPause:       body.CanPause,
			CanPlay:        body.CanPlay,
			CanSeek:        body.CanSeek,
			PlaybackStatus: body.PlaybackStatus,
			Shuffle:        body.Shuffle,
			LoopStatus:     body.LoopStatus,
		}
		p.mu.Lock()
		tracker, ok := p.positionTrackers[dev.ID()]
		if !ok {
			tracker = &remotePositionTracker{}
			p.positionTrackers[dev.ID()] = tracker
		}
		tracker.lastPosition = body.Pos
		tracker.lastPositionAt = time.Now()
		tracker.playing = body.IsPlaying
		state.PosAnchorMs = tracker.lastPositionAt.UnixMilli()
		shouldPublish := shouldPublishRemoteState(p.remoteStates[dev.ID()], state)
		p.remoteStates[dev.ID()] = state
		p.remoteStateTimes[dev.ID()] = time.Now()
		p.mu.Unlock()

		// Request art bytes from the phone when the advertsed album art is
		// a kdeconnect:// URI we have not cached yet. Resolve any already
		// cached art before publishing so watch clients get a loadable URL.
		if p.artCache != nil && p.artCache.Resolve(state.AlbumArtUrl) == "" {
			go p.requestAlbumArt(dev, state.Player, state.AlbumArtUrl)
		}
		if shouldPublish && p.bus != nil {
			pub := state.DeepCopy()
			if p.artCache != nil {
				if resolved := p.artCache.Resolve(pub.AlbumArtUrl); resolved != "" {
					pub.AlbumArtUrl = resolved
				} else if pub.AlbumArtUrl != "" && strings.HasPrefix(pub.AlbumArtUrl, "kdeconnect:") {
					// Art still downloading: publish an empty URL with the
					// pending flag instead of an unloadable kdeconnect:/
					// URI. The arrival re-publish carries file://.
					pub.AlbumArtUrl = ""
					pub.ArtPending = true
				}
			}
			p.bus.Publish(events.TypeMprisUpdate, dev.ID(), pub)
		}
		return nil
	}

	return nil
}

func shouldPublishRemoteState(last, current *NowPlaying) bool {
	if last == nil {
		return true
	}
	return last.Player != current.Player ||
		last.Title != current.Title ||
		last.Artist != current.Artist ||
		last.Album != current.Album ||
		last.AlbumArtUrl != current.AlbumArtUrl ||
		last.PlaybackStatus != current.PlaybackStatus ||
		last.IsPlaying != current.IsPlaying ||
		last.Volume != current.Volume
}
