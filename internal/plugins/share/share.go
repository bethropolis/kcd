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
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

type progressThrottle struct {
	bus      *events.Bus
	deviceID string
	filename string
	total    int64
	interval time.Duration
	mu       sync.Mutex
	last     time.Time
	pending  int64
}

func newProgressThrottle(bus *events.Bus, deviceID, filename string, total int64) *progressThrottle {
	return &progressThrottle{
		bus:      bus,
		deviceID: deviceID,
		filename: filename,
		total:    total,
		interval: 500 * time.Millisecond,
	}
}

func (t *progressThrottle) Update(current, _ int64) {
	if t.bus == nil {
		return
	}
	t.mu.Lock()
	t.pending = current
	now := time.Now()
	if now.Sub(t.last) < t.interval {
		t.mu.Unlock()
		return
	}
	t.last = now
	cur := t.pending
	t.mu.Unlock()

	t.bus.Publish(events.TypeShareProgress, t.deviceID, map[string]any{
		"file":    t.filename,
		"current": cur,
		"total":   t.total,
	})
}

type SharePlugin struct {
	DownloadDir string
	cfg         config.ShareConfig
	TLSConfig   *tls.Config
	Logger      *zap.Logger
	bus         *events.Bus
}

func NewSharePlugin(downloadDir string, cfg config.ShareConfig, tlsConfig *tls.Config, bus *events.Bus, logger *zap.Logger) *SharePlugin {
	return &SharePlugin{
		DownloadDir: downloadDir,
		cfg:         cfg,
		TLSConfig:   tlsConfig,
		Logger:      logger.With(zap.String("plugin", "share")),
		bus:         bus,
	}
}

// ShareBody includes the missing Android metadata (LastModified/CreationTime)
type ShareBody struct {
	Filename         string `json:"filename"`
	NumberOfFiles    int    `json:"numberOfFiles,omitempty"`
	TotalPayloadSize int64  `json:"totalPayloadSize,omitempty"`
	LastModified     int64  `json:"lastModified,omitempty"`
	CreationTime     int64  `json:"creationTime,omitempty"`
	Text             string `json:"text,omitempty"`
	Url              string `json:"url,omitempty"`
}

func (p *SharePlugin) Name() string { return "Share" }

func (p *SharePlugin) Timeout() time.Duration { return 0 }

func (p *SharePlugin) IncomingTypes() []string {
	return []string{"kdeconnect.share.request"}
}

func (p *SharePlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.share.request"}
}

func (p *SharePlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body ShareBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return fmt.Errorf("share: parse body: %w", err)
	}

	if body.Text != "" && pkt.PayloadSize <= 0 {
		p.Logger.Info("share: received text", zap.String("text", body.Text))
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
		p.Logger.Info("share: received url", zap.String("url", body.Url))
		if p.bus != nil {
			p.bus.Publish(events.TypeShareURL, dev.ID(), map[string]string{"url": body.Url})
		}
		plugin.RunCommandAsync(p.Logger, "xdg-open", body.Url)
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

	go func() {
		defer debug.FreeOSMemory()

		var onProgress func(int64, int64)
		if p.bus != nil {
			throttle := newProgressThrottle(p.bus, dev.ID(), body.Filename, payloadSize)
			onProgress = throttle.Update
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
			if p.cfg.AutoOpen {
				cmd := p.cfg.OpenCommand
				if cmd == "" {
					cmd = "xdg-open"
				}
				plugin.RunCommandAsync(p.Logger, cmd, destPath)
			}
		}
	}()

	return nil
}

func (p *SharePlugin) SendFile(ctx context.Context, dev device.Sender, filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("share: open file: %w", err)
	}
	stat, err := f.Stat()
	f.Close() // Close immediately; AcceptAndSend opens it again exactly when the phone connects.
	if err != nil {
		return fmt.Errorf("share: stat file: %w", err)
	}

	if stat.IsDir() {
		return fmt.Errorf("share: directory transfer not supported yet")
	}

	// Bind to an available side-channel port (using config range)
	ln, port, err := ListenSideChannel(ctx, p.cfg, p.TLSConfig)
	if err != nil {
		return err
	}

	var onProgress func(int64, int64)
	if p.bus != nil {
		throttle := newProgressThrottle(p.bus, dev.ID(), filepath.Base(filePath), stat.Size())
		onProgress = throttle.Update
	}

	// Handle the transfer in the background so IPC returns instantly
	go func() {
		defer debug.FreeOSMemory()

		timeout := time.Duration(p.cfg.AcceptTimeoutSecs) * time.Second
		if timeout == 0 {
			timeout = 2 * time.Minute
		}
		err := AcceptAndSend(ln, filePath, p.TLSConfig, dev.ID(), timeout, onProgress, p.Logger)

		if err != nil {
			p.Logger.Error("share: send failed",
				zap.String("device_id", dev.ID()),
				zap.String("file", filepath.Base(filePath)),
				zap.Int("port", port),
				zap.Error(err),
			)
		} else {
			p.Logger.Info("share: send complete",
				zap.String("device_id", dev.ID()),
				zap.String("file", filepath.Base(filePath)),
			)
		}

		if p.bus != nil {
			payload := map[string]interface{}{
				"file":    filepath.Base(filePath),
				"success": err == nil,
			}
			if err != nil {
				payload["error"] = err.Error()
			}
			p.bus.Publish(events.TypeShareComplete, dev.ID(), payload)
		}
	}()

	modTime := stat.ModTime().UnixMilli()

	// Send invite packet with strict metadata
	pkt, err := protocol.NewPacket("kdeconnect.share.request", ShareBody{
		Filename:         filepath.Base(filePath),
		NumberOfFiles:    1,
		TotalPayloadSize: stat.Size(),
		LastModified:     modTime,
		CreationTime:     modTime,
	})
	if err != nil {
		ln.Close()
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

	return dev.Send(pkt)
}

func (p *SharePlugin) OnConnect(dev device.Sender)    {}
func (p *SharePlugin) OnDisconnect(dev device.Sender) {}
