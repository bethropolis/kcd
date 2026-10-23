package share

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	desktopclipboard "github.com/bethropolis/kcd/internal/desktop/clipboard"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/sysexec"
)

func (p *SharePlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body ShareBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return fmt.Errorf("share: parse body: %w", err)
	}

	if body.Text != "" && pkt.PayloadTransferInfo == nil {
		p.Logger.Info("share: received text", log.String("text", body.Text))
		if p.bus != nil {
			p.bus.Publish(events.TypeShareText, dev.ID(), map[string]string{"text": body.Text})
		}
		go func() {
			if err := desktopclipboard.SetText(context.Background(), body.Text, nil); err != nil {
				p.Logger.Warn("share: clipboard update failed", log.Error(err))
			}
		}()
		return nil
	}

	if body.Url != "" && pkt.PayloadTransferInfo == nil {
		p.Logger.Info("share: received url", log.String("url", body.Url))
		if p.bus != nil {
			p.bus.Publish(events.TypeShareURL, dev.ID(), map[string]string{"url": body.Url})
		}
		// Only http(s) may reach xdg-open (other schemes hit unrelated
		// handlers); the URL event still emits either way.
		if isOpenableURL(body.Url) {
			sysexec.RunAsync(p.Logger, "xdg-open", body.Url)
		} else {
			p.Logger.Warn("share: refusing to open non-http(s) URL",
				log.String("url", body.Url))
		}
		return nil
	}

	if pkt.PayloadSize < -1 || pkt.PayloadTransferInfo == nil {
		return nil
	}
	if pkt.PayloadSize > p.cfg.MaxFileSize {
		return fmt.Errorf("share: payload size %d exceeds configured limit %d", pkt.PayloadSize, p.cfg.MaxFileSize)
	}

	safeName := SanitizeFilename(body.Filename)
	if err := os.MkdirAll(p.DownloadDir, 0755); err != nil {
		return fmt.Errorf("share: critical - failed to create download dir %s: %w", p.DownloadDir, err)
	}

	destPath, err := EnsureUnique(p.DownloadDir, safeName)
	if p.cfg.Overwrite {
		destPath = filepath.Join(p.DownloadDir, safeName)
		err = nil
	}
	if err != nil {
		return fmt.Errorf("share: collision handling: %w", err)
	}

	remoteIP := dev.RemoteIP()
	if remoteIP == nil {
		return fmt.Errorf("share: failed to resolve remote peer IP")
	}

	payloadSize := pkt.PayloadSize
	payloadPort := pkt.PayloadTransferInfo.Port
	expectedFP := cert.PinnedFingerprint(dev.PeerCert())

	go func() {
		defer debug.FreeOSMemory()

		var onProgress func(int64, int64)
		if p.bus != nil {
			throttle := newProgressThrottle(p.bus, dev.ID(), body.Filename, payloadSize)
			onProgress = throttle.Update
		}

		err := ReceiveSideChannel(context.Background(), remoteIP, payloadPort, payloadSize, destPath, p.TLSConfig, expectedFP, onProgress, p.Logger, p.sidechannel)
		if err != nil {
			p.Logger.Error("share receive failed", log.Error(err))
			if p.bus != nil {
				p.bus.Publish(events.TypeShareComplete, dev.ID(), map[string]interface{}{
					"file":    body.Filename,
					"success": false,
					"error":   err.Error(),
				})
			}
		} else {
			if body.LastModified > 0 {
				modTime := time.UnixMilli(body.LastModified)
				if err := os.Chtimes(destPath, modTime, modTime); err != nil {
					p.Logger.Debug("share: failed to restore file timestamps", log.Error(err))
				}
			}

			if p.bus != nil {
				p.bus.Publish(events.TypeShareComplete, dev.ID(), map[string]interface{}{
					"file":    body.Filename,
					"success": true,
				})
			}
			if p.cfg.AutoOpen {
				// Never auto-open executable content (code execution); the file
				// is still saved and announced for manual opening.
				if autoOpenBlocked(destPath) {
					p.Logger.Warn("share: refusing to auto-open executable file",
						log.String("file", destPath))
				} else {
					cmd := p.cfg.OpenCommand
					if cmd == "" {
						cmd = "xdg-open"
					}
					absPath, err := filepath.Abs(destPath)
					if err != nil {
						absPath = destPath
					}
					sysexec.RunAsync(p.Logger, cmd, absPath)
				}
			}
		}
	}()

	return nil
}
