package share

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// SharePlugin handles file transfers.
type SharePlugin struct {
	DownloadDir string
	TLSConfig   *tls.Config
	Logger      *zap.Logger
	bus         *events.Bus
}

// NewSharePlugin creates a new SharePlugin instance.
func NewSharePlugin(downloadDir string, tlsConfig *tls.Config, bus *events.Bus, logger *zap.Logger) *SharePlugin {
	return &SharePlugin{
		DownloadDir: downloadDir,
		TLSConfig:   tlsConfig,
		Logger:      logger.With(zap.String("plugin", "share")),
		bus:         bus,
	}
}

// ShareBody represents the body of a kdeconnect.share.request packet.
type ShareBody struct {
	Filename         string `json:"filename"`
	NumberOfFiles    int    `json:"numberOfFiles,omitempty"`
	TotalPayloadSize int64  `json:"totalPayloadSize,omitempty"`
	Text             string `json:"text,omitempty"`
	Url              string `json:"url,omitempty"`
}

func (p *SharePlugin) Name() string { return "Share" }

func (p *SharePlugin) Timeout() time.Duration { return 0 } // No timeout for file transfer setup

// IncomingTypes returns the packet types this plugin handles.
func (p *SharePlugin) IncomingTypes() []string {
	return []string{"kdeconnect.share.request"}
}

// OutgoingTypes returns the packet types this plugin may send.
func (p *SharePlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.share.request"}
}

// Handle processes incoming share requests by initiating a side-channel transfer.
func (p *SharePlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body ShareBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return fmt.Errorf("share: parse body: %w", err)
	}

	// Text share (no payload, has "text" field)
	if body.Text != "" && pkt.PayloadSize <= 0 {
		p.Logger.Info("share: received text", zap.String("text", body.Text))
		if p.bus != nil {
			p.bus.Publish(events.TypeShareText, dev.ID(), map[string]string{"text": body.Text})
		}
		go func() {
			var cmd *exec.Cmd
			if strings.Contains(os.Getenv("WAYLAND_DISPLAY"), "") && os.Getenv("WAYLAND_DISPLAY") != "" {
				cmd = exec.Command("wl-copy")
			} else {
				cmd = exec.Command("xclip", "-selection", "clipboard")
			}
			cmd.Stdin = strings.NewReader(body.Text)
			_ = cmd.Run()
		}()
		return nil
	}

	// URL share (no payload, has "url" field)
	if body.Url != "" && pkt.PayloadSize <= 0 {
		p.Logger.Info("share: received url", zap.String("url", body.Url))
		if p.bus != nil {
			p.bus.Publish(events.TypeShareURL, dev.ID(), map[string]string{"url": body.Url})
		}
		plugin.RunCommandAsync(p.Logger, "xdg-open", body.Url)
		return nil
	}

	// File share — requires payload
	if pkt.PayloadSize <= 0 || pkt.PayloadTransferInfo == nil {
		p.Logger.Debug("ignoring share request with no payload metadata")
		return nil
	}

	// Security: Sanitize the filename to prevent path traversal per absolute rule.
	safeName := SanitizeFilename(body.Filename)

	// Ensure the download directory exists.
	if err := os.MkdirAll(p.DownloadDir, 0755); err != nil {
		return fmt.Errorf("share: critical - failed to create download dir %s: %w", p.DownloadDir, err)
	}

	// Security: Don't overwrite existing files; generate a unique name per absolute rule.
	destPath, err := EnsureUnique(p.DownloadDir, safeName)
	if err != nil {
		return fmt.Errorf("share: collision handling: %w", err)
	}

	// We need the remote IP to dial the side-channel.
	remoteIP := dev.RemoteIP()
	if remoteIP == nil {
		return fmt.Errorf("share: failed to resolve remote peer IP")
	}

	// Copy values we need before spawning goroutine.
	// The packet will be released back to the pool after Handle() returns,
	// so we must not reference pkt.* inside the goroutine.
	payloadSize := pkt.PayloadSize
	payloadPort := pkt.PayloadTransferInfo.Port

	// Handle() must not block per absolute rule. Spawning goroutine for transfer.
	go func() {
		defer debug.FreeOSMemory() // Free memory after large file transfer per Phase 3 rules

		var onProgress func(int64, int64)
		if p.bus != nil {
			onProgress = func(current, total int64) {
				p.bus.Publish(events.TypeShareProgress, dev.ID(), map[string]interface{}{
					"file":    body.Filename,
					"current": current,
					"total":   total,
				})
			}
		}

		err := ReceiveSideChannel(context.Background(), remoteIP, payloadPort, payloadSize, destPath, p.TLSConfig, onProgress, p.Logger)
		if err != nil {
			p.Logger.Error("share receive failed", zap.Error(err))
			if p.bus != nil {
				p.bus.Publish(events.TypeShareComplete, dev.ID(), map[string]interface{}{
					"file":    body.Filename,
					"success": false,
					"error":   err.Error(),
				})
			}
		} else {
			if p.bus != nil {
				p.bus.Publish(events.TypeShareComplete, dev.ID(), map[string]interface{}{
					"file":    body.Filename,
					"success": true,
				})
			}
		}
	}()

	return nil
}

// SendFile prepares and initiates an outbound file transfer.
func (p *SharePlugin) SendFile(ctx context.Context, dev device.Sender, filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("share: open file: %w", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("share: stat file: %w", err)
	}

	if stat.IsDir() {
		return fmt.Errorf("share: directory transfer not supported yet")
	}

	// Start side-channel listener
	var onProgress func(int64, int64)
	if p.bus != nil {
		onProgress = func(current, total int64) {
			p.bus.Publish(events.TypeShareProgress, dev.ID(), map[string]interface{}{
				"file":    filepath.Base(filePath),
				"current": current,
				"total":   total,
			})
		}
	}

	port, err := SendSideChannel(ctx, filePath, p.TLSConfig, dev.ID(), onProgress, p.Logger)
	if err != nil {
		return err
	}

	// Construct share invite packet
	pkt, err := protocol.NewPacket("kdeconnect.share.request", ShareBody{
		Filename:         filepath.Base(filePath),
		NumberOfFiles:    1,
		TotalPayloadSize: stat.Size(),
	})
	if err != nil {
		return err
	}

	pkt.PayloadSize = stat.Size()
	pkt.PayloadTransferInfo = &protocol.TransferInfo{
		Port: port,
	}

	p.Logger.Info("share: sending transfer invitation",
		zap.String("device_id", dev.ID()),
		zap.String("path", filePath),
		zap.Int64("size", pkt.PayloadSize),
		zap.Int("port", port),
	)

	// All outgoing packets must go through Device.Send per absolute rule.
	return dev.Send(pkt)
}

func (p *SharePlugin) OnConnect(dev device.Sender) {}

func (p *SharePlugin) OnDisconnect(dev device.Sender) {
}
