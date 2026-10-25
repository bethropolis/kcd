package mpris

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
)

func cleanArtURL(rawURL string) (string, error) {
	if !strings.HasPrefix(rawURL, "file://") {
		return "", fmt.Errorf("not a file:// URL")
	}
	clean := rawURL
	if idx := strings.LastIndex(clean, "?t="); idx != -1 {
		clean = clean[:idx]
	}
	filePath := strings.TrimPrefix(clean, "file://")
	unescaped, err := url.PathUnescape(filePath)
	if err != nil {
		return "", fmt.Errorf("unescape path: %w", err)
	}
	filePath = filepath.Clean(unescaped)
	if !filepath.IsAbs(filePath) {
		return "", fmt.Errorf("artwork path is not absolute")
	}
	return filePath, nil
}

func (p *MPRISPlugin) authorizeOutboundArt(player, rawRequestedURL string) (string, error) {
	reqPath, err := cleanArtURL(rawRequestedURL)
	if err != nil {
		return "", err
	}

	var targetDisplayName string
	if player != "" {
		pl := p.resolvePlayer(player)
		if pl == nil {
			return "", fmt.Errorf("unknown player %q", player)
		}
		targetDisplayName = pl.displayName
	}

	p.mu.RLock()
	var candidates []*NowPlaying
	if targetDisplayName != "" {
		if state := p.lastStates[targetDisplayName]; state != nil {
			candidates = append(candidates, state)
		}
	} else {
		for _, state := range p.lastStates {
			if state != nil {
				candidates = append(candidates, state)
			}
		}
	}
	p.mu.RUnlock()

	for _, state := range candidates {
		if state.AlbumArtUrl == "" {
			continue
		}
		advertisedPath, err := cleanArtURL(state.AlbumArtUrl)
		if err != nil || advertisedPath != reqPath {
			continue
		}
		info, err := os.Lstat(advertisedPath)
		if err != nil {
			return "", fmt.Errorf("stat artwork: %w", err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("artwork %q is not a regular file", advertisedPath)
		}
		if info.Size() > maxAlbumArtBytes {
			return "", fmt.Errorf("artwork %q exceeds size limit", advertisedPath)
		}
		return advertisedPath, nil
	}

	return "", fmt.Errorf("requested path %q is not advertised by an active player", reqPath)
}

func (p *MPRISPlugin) sendAlbumArt(dev device.Sender, player, artUrl string) {
	filePath, err := p.authorizeOutboundArt(player, artUrl)
	if err != nil {
		p.logger.Warn("mpris: reject unauthorized album art request",
			log.String("player", player), log.String("art_url", artUrl), log.Error(err))
		return
	}

	p.mu.Lock()
	reqKey := dev.ID() + "|" + artUrl
	if lastReq, exists := p.artRequests[reqKey]; exists && time.Since(lastReq) < 5*time.Second {
		p.mu.Unlock()
		return
	}
	p.artRequests[reqKey] = time.Now()
	p.mu.Unlock()

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

	portMin, portMax := p.sidechannel.PortMin, p.sidechannel.PortMax
	if portMin == 0 {
		portMin = protocol.DefaultSidechannelPortMin
	}
	if portMax == 0 {
		portMax = protocol.DefaultSidechannelPortMax
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ln, port, err := transport.ListenSidechannel(ctx, portMin, portMax)
	if err != nil {
		return
	}

	go func() {
		_ = transport.AcceptAndSend(ln, filePath, p.tlsConfig, dev.ID(), cert.PinnedFingerprint(dev.PeerCert()), 10*time.Second, nil, p.logger, portMin, portMax, p.sidechannel)
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

// receiveAlbumArt streams an inbound art payload into the cache and
// re-publishes state with the resolved file:// URL for watchers.
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
	if _, err := transport.ReceiveToFile(dlCtx, remoteIP, port, size, tmpPath, p.tlsConfig, cert.PinnedFingerprint(dev.PeerCert()), p.logger, transport.ReceiveOptions{MaxSize: maxAlbumArtBytes, Sidechannel: p.sidechannel}); err != nil {
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

// stampAlbumArt publishes a resolved art URL only if the current track still
// wants exactly this art; the download may outlive the track that asked.
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
