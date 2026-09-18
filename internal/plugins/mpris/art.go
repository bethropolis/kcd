package mpris

import (
	"context"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugins/share"
	"github.com/bethropolis/kcd/internal/protocol"
)

func (p *MPRISPlugin) sendAlbumArt(ctx context.Context, dev device.Sender, player, artUrl string) {
	p.mu.Lock()
	reqKey := dev.ID() + "|" + artUrl
	if lastReq, exists := p.artRequests[reqKey]; exists && time.Since(lastReq) < 5*time.Second {
		p.mu.Unlock()
		return
	}
	p.artRequests[reqKey] = time.Now()
	p.mu.Unlock()

	cleanUrl := artUrl
	if idx := strings.LastIndex(cleanUrl, "?t="); idx != -1 {
		cleanUrl = cleanUrl[:idx]
	}

	filePath := strings.TrimPrefix(cleanUrl, "file://")
	if unescaped, err := url.PathUnescape(filePath); err == nil {
		filePath = unescaped
	}

	f, err := os.Open(filePath)
	if err != nil {
		p.logger.Debug("mpris: album art file not found", log.String("path", filePath), log.Error(err))
		return
	}
	stat, err := f.Stat()
	f.Close()
	if err != nil {
		return
	}

	var shareCfg config.ShareConfig
	shareCfg.Defaults()

	ln, port, err := share.ListenSideChannel(ctx, shareCfg, p.tlsConfig)
	if err != nil {
		return
	}

	go func() {
		_ = share.AcceptAndSend(ln, filePath, p.tlsConfig, dev.ID(), cert.PinnedFingerprint(dev.PeerCert()), 10*time.Second, nil, p.logger)
	}()

	pkt, err := protocol.NewPacket("kdeconnect.mpris", map[string]interface{}{
		"transferringAlbumArt": true,
		"player":               player,
		"albumArtUrl":          artUrl,
	})
	if err == nil {
		pkt.PayloadSize = stat.Size()
		pkt.PayloadTransferInfo = &protocol.TransferInfo{Port: port}
		dev.Send(pkt)
	}
}

// requestAlbumArt asks the remote device to stream the album art bytes
// referenced by a kdeconnect:/artUri URI over a side channel.
func (p *MPRISPlugin) requestAlbumArt(dev device.Sender, player, artUrl string) {
	if player == "" || artUrl == "" {
		return
	}
	p.mu.Lock()
	reqKey := "req|" + dev.ID() + "|" + artUrl
	if lastReq, exists := p.artRequests[reqKey]; exists && time.Since(lastReq) < 10*time.Second {
		p.mu.Unlock()
		return
	}
	p.artRequests[reqKey] = time.Now()
	p.mu.Unlock()

	body := MPRISRequest{
		Player:      player,
		AlbumArtUrl: artUrl,
	}
	pkt, err := protocol.NewPacket("kdeconnect.mpris.request", body)
	if err != nil {
		return
	}
	if err := dev.Send(pkt); err != nil {
		p.logger.Debug("mpris: album art request failed", log.Error(err))
	}
}

// receiveAlbumArt streams an inbound album art payload into the cache
// and re-publishes the device state with a resolved file:// URL so watch
// clients and kcd mpris status surface the loadable location.
func (p *MPRISPlugin) receiveAlbumArt(_ context.Context, dev device.Sender, player, artUrl string, size int64, port int) {
	remoteIP := dev.RemoteIP()
	if remoteIP == nil {
		return
	}
	if p.artCache == nil || size <= 0 || size > maxAlbumArtBytes {
		return
	}

	p.logger.Debug("mpris: receiving album art from remote",
		log.String("player", player),
		log.String("device_id", dev.ID()),
		log.Int64("size", size))

	tmp, err := os.CreateTemp(p.artCache.Dir(), ".art-*")
	if err != nil {
		p.logger.Warn("mpris: failed to create temp file for album art", log.Error(err))
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	// The Handle ctx is canceled as soon as Handle returns; use an
	// independent context so the side-channel dial isn't aborted.
	dlCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := share.ReceiveSideChannel(dlCtx, remoteIP, port, size, tmpPath, p.tlsConfig, cert.PinnedFingerprint(dev.PeerCert()), nil, p.logger); err != nil {
		p.logger.Warn("mpris: album art transfer failed", log.Error(err))
		return
	}

	fileURL, err := p.artCache.Commit(artUrl, tmpPath)
	if err != nil {
		p.logger.Warn("mpris: failed to cache album art", log.Error(err))
		return
	}

	p.stampAlbumArt(dev.ID(), player, artUrl, fileURL)
}

// stampAlbumArt publishes a resolved file:// URL for a completed album-art
// fetch — but only if the device's current track still wants exactly this
// art. The side-channel download can take up to 30s, during which the track
// (or the active player) may have changed; stamping blindly would show the
// old track's cover on the new track. On mismatch the bytes stay in the art
// cache and the new track's own art request fulfills it.
func (p *MPRISPlugin) stampAlbumArt(deviceID, player, artUrl, fileURL string) {
	p.mu.Lock()
	state := p.remoteStates[deviceID]
	if state == nil || state.AlbumArtUrl != artUrl || state.Player != player {
		p.mu.Unlock()
		return
	}
	state = state.DeepCopy()
	state.AlbumArtUrl = fileURL
	p.mu.Unlock()
	if p.bus != nil {
		p.bus.Publish(events.TypeMprisUpdate, deviceID, state)
	}
}
