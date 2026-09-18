package share

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
)

func (p *SharePlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body ShareBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return fmt.Errorf("share: parse body: %w", err)
	}

	if body.Text != "" && pkt.PayloadSize <= 0 {
		p.Logger.Info("share: received text", log.String("text", body.Text))
		if p.bus != nil {
			p.bus.Publish(events.TypeShareText, dev.ID(), map[string]string{"text": body.Text})
		}
		go func() {
			var cmd *exec.Cmd
			if os.Getenv("WAYLAND_DISPLAY") != "" {
				cmd = exec.CommandContext(context.Background(), "wl-copy")
			} else {
				cmd = exec.CommandContext(context.Background(), "xclip", "-selection", "clipboard")
			}
			cmd.Stdin = strings.NewReader(body.Text)
			_ = cmd.Run()
		}()
		return nil
	}

	if body.Url != "" && pkt.PayloadSize <= 0 {
		p.Logger.Info("share: received url", log.String("url", body.Url))
		if p.bus != nil {
			p.bus.Publish(events.TypeShareURL, dev.ID(), map[string]string{"url": body.Url})
		}
		// xdg-open dispatches on URI scheme to arbitrary desktop handlers,
		// so only http(s) may reach it: file://, smb:, mailto: and custom
		// app schemes would hand phone-influenced input to unrelated local
		// handlers. The URL event above still reaches clients either way.
		if isOpenableURL(body.Url) {
			plugin.RunCommandAsync(p.Logger, "xdg-open", body.Url)
		} else {
			p.Logger.Warn("share: refusing to open non-http(s) URL",
				log.String("url", body.Url))
		}
		return nil
	}

	if pkt.PayloadSize <= 0 || pkt.PayloadTransferInfo == nil {
		return nil
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
				// Never auto-open executable content: handing .desktop files
				// (or scripts) to the desktop handler can execute code. The
				// file itself is still saved and announced — open it manually.
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
					plugin.RunCommandAsync(p.Logger, cmd, absPath)
				}
			}
		}
	}()

	return nil
}
