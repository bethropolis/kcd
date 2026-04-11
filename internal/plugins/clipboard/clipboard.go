package clipboard

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// ClipboardPlugin handles clipboard sync both directions.
type ClipboardPlugin struct {
	lastTimestamp int64
	tlsConfig     *tls.Config
	logger        *zap.Logger
	isWayland     bool
	mu            sync.Mutex
	lastContent   string
}

// NewClipboardPlugin creates a clipboard plugin.
func NewClipboardPlugin(tlsConfig *tls.Config, logger *zap.Logger) *ClipboardPlugin {
	return &ClipboardPlugin{
		tlsConfig: tlsConfig,
		logger:    logger.With(zap.String("plugin", "clipboard")),
		isWayland: os.Getenv("WAYLAND_DISPLAY") != "",
	}
}

// ClipboardBody represents the content of a clipboard packet.
type ClipboardBody struct {
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp,omitempty"`
}

// Name returns the plugin name.
func (p *ClipboardPlugin) Name() string { return "Clipboard" }

// Timeout returns the timeout.
func (p *ClipboardPlugin) Timeout() time.Duration { return 5 * time.Second }

// IncomingTypes returns the packet types this plugin handles.
func (p *ClipboardPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.clipboard", "kdeconnect.clipboard.connect", "kdeconnect.clipboard.file"}
}

// OutgoingTypes returns the packet types this plugin may send.
func (p *ClipboardPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.clipboard"}
}

// Handle processes incoming clipboard packets.
func (p *ClipboardPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	// Handle image/file clipboard transfer
	if pkt.Type == "kdeconnect.clipboard.file" {
		return p.handleClipboardFile(ctx, dev, pkt)
	}

	var body ClipboardBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		// Ignore if it's just connectivity notification (kdeconnect.clipboard.connect)
		// but failed to parse content.
		return nil
	}

	if body.Content == "" {
		return nil
	}

	p.mu.Lock()
	if body.Content == p.lastContent {
		p.mu.Unlock()
		return nil
	}
	p.lastContent = body.Content
	p.mu.Unlock()

	if body.Timestamp > 0 {
		if body.Timestamp < p.lastTimestamp {
			return nil
		}
		p.lastTimestamp = body.Timestamp
	}

	// Spawning goroutine as Handlers must not block.
	go func() {
		var cmd *exec.Cmd
		// Automatically detect tool by env per absolute rule.
		if p.isWayland {
			cmd = exec.Command("wl-copy")
		} else {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		}

		cmd.Stdin = strings.NewReader(body.Content)
		_ = cmd.Run()
	}()

	return nil
}

// ClipboardFileBody is the body of kdeconnect.clipboard.file.
type ClipboardFileBody struct {
	Filename string `json:"filename"`
}

func (p *ClipboardPlugin) handleClipboardFile(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	if pkt.PayloadSize <= 0 || pkt.PayloadTransferInfo == nil {
		return nil
	}
	var body ClipboardFileBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil || body.Filename == "" {
		return nil
	}

	remoteIP := dev.RemoteIP()
	if remoteIP == nil {
		return nil
	}

	payloadSize := pkt.PayloadSize
	payloadPort := pkt.PayloadTransferInfo.Port
	filename := body.Filename

	go func() {
		// Download to a temp file
		tmpFile, err := os.CreateTemp("", "kcd-clip-*"+filepath.Ext(filename))
		if err != nil {
			p.logger.Error("clipboard file: failed to create temp file", zap.Error(err))
			return
		}
		tmpPath := tmpFile.Name()
		tmpFile.Close()
		defer os.Remove(tmpPath)

		if err := downloadToFile(ctx, remoteIP, payloadPort, payloadSize, tmpPath, p.tlsConfig, p.logger); err != nil {
			p.logger.Error("clipboard file: download failed", zap.Error(err))
			return
		}

		// Detect MIME type from extension
		mimeType := mime.TypeByExtension(filepath.Ext(filename))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		t, err := os.Open(tmpPath)
		if err != nil {
			return
		}
		defer t.Close()

		var cmd *exec.Cmd
		if p.isWayland {
			cmd = exec.Command("wl-copy", "--type", mimeType)
		} else {
			cmd = exec.Command("xclip", "-selection", "clipboard", "-t", mimeType, "-i")
		}
		cmd.Stdin = t
		if out, err := cmd.CombinedOutput(); err != nil {
			p.logger.Warn("clipboard file: failed to set clipboard", zap.Error(err), zap.String("output", string(out)))
		}
	}()

	return nil
}

// downloadToFile dials a TLS side-channel and streams the payload to dest.
func downloadToFile(ctx context.Context, ip net.IP, port int, size int64, dest string, tlsConfig *tls.Config, logger *zap.Logger) error {
	addr := fmt.Sprintf("%s:%d", ip.String(), port)
	dialer := &tls.Dialer{Config: tlsConfig}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("clipboard: dial %s: %w", addr, err)
	}
	defer conn.Close()

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("clipboard: create file %s: %w", dest, err)
	}
	defer f.Close()

	_, err = io.Copy(f, io.LimitReader(conn, size))
	if err != nil {
		return fmt.Errorf("clipboard: stream to %s: %w", dest, err)
	}
	return nil
}

// Push copies the local clipboard to the remote device using wl-paste or xclip -o.
func Push(ctx context.Context, dev device.Sender, p *ClipboardPlugin) error {
	var cmd *exec.Cmd
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		cmd = exec.Command("wl-paste", "-n")
	} else {
		cmd = exec.Command("xclip", "-selection", "clipboard", "-o")
	}

	out, err := cmd.Output()
	if err != nil {
		return err
	}

	p.mu.Lock()
	if string(out) == p.lastContent {
		p.mu.Unlock()
		return nil
	}
	p.lastContent = string(out)
	p.mu.Unlock()

	pkt, err := protocol.NewPacket("kdeconnect.clipboard", ClipboardBody{
		Content: string(out),
	})
	if err != nil {
		return err
	}

	// All outgoing packets must use device.Send.
	return dev.Send(pkt)
}

func (p *ClipboardPlugin) OnConnect(dev device.Sender) {}

func (p *ClipboardPlugin) OnDisconnect(dev device.Sender) {
}
