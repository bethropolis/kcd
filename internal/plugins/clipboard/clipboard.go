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

type clipboardBackend int

const (
	backendUnknown clipboardBackend = iota
	backendWayland
	backendX11
)

// ClipboardPlugin handles clipboard sync both directions.
type ClipboardPlugin struct {
	pushOnConnect     bool
	lastTimestamp     int64
	tlsConfig         *tls.Config
	logger            *zap.Logger
	backend           clipboardBackend
	wlDisplay         string // WAYLAND_DISPLAY value for spawned subprocesses
	probe             func() (clipboardBackend, string)
	mu                sync.Mutex
	lastContent       string // last content received from phone (inbound)
	lastPushedContent string // last content sent to phone (outbound)
}

// NewClipboardPlugin creates a clipboard plugin.
func NewClipboardPlugin(tlsConfig *tls.Config, logger *zap.Logger, pushOnConnect bool) *ClipboardPlugin {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ClipboardPlugin{
		tlsConfig:     tlsConfig,
		pushOnConnect: pushOnConnect,
		logger:        logger.With(zap.String("plugin", "clipboard")),
		probe:         probeBackend,
	}
}

// probeBackend determines the usable clipboard backend (wl-paste/xclip) by
// inspecting the environment and $XDG_RUNTIME_DIR. It is side-effect free and
// unit-testable. The returned string is the WAYLAND_DISPLAY value to inject
// into spawned subprocesses (empty for X11/unknown).
//
// A Wayland socket is preferred over DISPLAY: under a systemd user service
// WAYLAND_DISPLAY is often unset at startup, and treating DISPLAY as the
// backend on a Wayland session silently copies to the X clipboard where
// Wayland-native apps never see it.
func probeBackend() (clipboardBackend, string) {
	rtDir := os.Getenv("XDG_RUNTIME_DIR")

	// Wayland: trust WAYLAND_DISPLAY only if its socket actually exists,
	// otherwise scan the runtime dir for any live wayland-* socket.
	if disp := os.Getenv("WAYLAND_DISPLAY"); disp != "" && rtDir != "" {
		if _, err := os.Stat(filepath.Join(rtDir, disp)); err == nil {
			if _, err := exec.LookPath("wl-paste"); err == nil {
				return backendWayland, disp
			}
		}
	}
	if rtDir != "" {
		if entries, err := os.ReadDir(rtDir); err == nil {
			for _, e := range entries {
				name := e.Name()
				if !e.IsDir() && strings.HasPrefix(name, "wayland-") {
					if _, err := exec.LookPath("wl-paste"); err == nil {
						return backendWayland, name
					}
				}
			}
		}
	}

	// X11 fallback.
	if os.Getenv("DISPLAY") != "" {
		if _, err := exec.LookPath("xclip"); err == nil {
			return backendX11, ""
		}
	}
	return backendUnknown, ""
}

// getBackend returns the cached backend, re-probing while it is unknown so an
// early failed probe (compositor not up yet, env not imported) does not stick
// for the lifetime of the process. Only non-unknown results are cached.
func (p *ClipboardPlugin) getBackend() (clipboardBackend, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.backend != backendUnknown {
		return p.backend, p.wlDisplay
	}
	backend, disp := p.probe()
	if backend != backendUnknown {
		p.backend = backend
		p.wlDisplay = disp
		p.logger.Debug("clipboard: backend detected",
			zap.Int("backend", int(backend)), zap.String("wl_display", disp))
	}
	return backend, disp
}

// clipboardCmd builds an exec.Cmd for a clipboard tool with a strict timeout
// (a hung wl-paste must not stall the daemon) and with WAYLAND_DISPLAY injected
// into the subprocess environment from the probed socket, so the tool works
// even when the daemon's own environment lacks the variable.
func (p *ClipboardPlugin) clipboardCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	ctx, cancel := context.WithTimeout(ctx, clipboardTimeout)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	if _, disp := p.getBackend(); disp != "" {
		cmd.Env = append(os.Environ(), "WAYLAND_DISPLAY="+disp)
	}
	_ = cancel // the context's timer owns the deadline; cancel fires on its own
	return cmd
}

// clipboardTimeout bounds every wl-copy/wl-paste/xclip subprocess so a hung
// clipboard tool cannot block clipboard sync indefinitely.
const clipboardTimeout = 2 * time.Second

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
	// Guard lastTimestamp under the same lock as lastContent — they form a
	// consistent pair and both fields are written from the TCP read goroutine.
	if body.Timestamp > 0 {
		if body.Timestamp < p.lastTimestamp {
			p.mu.Unlock()
			return nil
		}
		p.lastTimestamp = body.Timestamp
	}
	p.lastContent = body.Content
	p.mu.Unlock()

	// Spawning goroutine as Handlers must not block.
	go func() {
		var cmd *exec.Cmd
		switch backend, _ := p.getBackend(); backend {
		case backendWayland:
			cmd = p.clipboardCmd(context.Background(), "wl-copy")
		case backendX11:
			cmd = p.clipboardCmd(context.Background(), "xclip", "-selection", "clipboard")
		default:
			p.logger.Debug("clipboard: no backend available, dropping inbound copy")
			return
		}

		cmd.Stdin = strings.NewReader(body.Content)
		if err := cmd.Run(); err != nil {
			p.logger.Warn("clipboard: failed to set clipboard", zap.Error(err))
		}
	}()

	return nil
}

// ClipboardFileBody is the body of kdeconnect.clipboard.file.
type ClipboardFileBody struct {
	Filename string `json:"filename"`
}

const maxClipboardFileSize = 50 * 1024 * 1024 // 50 MB safety limit (matches KDE Connect C++)

func (p *ClipboardPlugin) handleClipboardFile(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	if pkt.PayloadSize <= 0 || pkt.PayloadTransferInfo == nil {
		return nil
	}

	if pkt.PayloadSize > maxClipboardFileSize {
		p.logger.Warn("clipboard file: rejected payload exceeding size limit",
			zap.Int64("size", pkt.PayloadSize),
			zap.Int("limit_bytes", maxClipboardFileSize),
		)
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
		switch backend, _ := p.getBackend(); backend {
		case backendWayland:
			cmd = p.clipboardCmd(context.Background(), "wl-copy", "--type", mimeType)
		case backendX11:
			cmd = p.clipboardCmd(context.Background(), "xclip", "-selection", "clipboard", "-t", mimeType, "-i")
		default:
			return
		}
		cmd.Stdin = t
		if out, err := cmd.CombinedOutput(); err != nil {
			p.logger.Warn("clipboard file: failed to set clipboard", zap.Error(err), zap.String("output", string(out)))
		}
	}()

	return nil
}

// downloadToFile dials a TLS side-channel and streams the payload to dest.
func downloadToFile(ctx context.Context, ip net.IP, port int, size int64, dest string, tlsConfig *tls.Config, _ *zap.Logger) error {
	addr := fmt.Sprintf("%s:%d", ip.String(), port)
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		},
		Config: tlsConfig,
	}
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
	switch backend, _ := p.getBackend(); backend {
	case backendWayland:
		cmd = p.clipboardCmd(ctx, "wl-paste", "-n")
	case backendX11:
		cmd = p.clipboardCmd(ctx, "xclip", "-selection", "clipboard", "-o")
	default:
		return fmt.Errorf("clipboard: no clipboard tool available")
	}

	out, err := cmd.Output()
	if err != nil {
		return err
	}

	content := string(out)

	p.mu.Lock()
	// Skip if content matches what we last received from the phone (lastContent)
	// OR what we last pushed outbound (lastPushedContent).
	//
	// lastContent guard: prevents sending the phone's own content back.
	// lastPushedContent guard: prevents duplicate pushes when the local
	// clipboard hasn't changed between two Push calls.
	if content == p.lastContent || content == p.lastPushedContent {
		p.mu.Unlock()
		return nil
	}
	p.lastPushedContent = content
	p.mu.Unlock()

	pkt, err := protocol.NewPacket("kdeconnect.clipboard", ClipboardBody{
		Content: content,
	})
	if err != nil {
		return err
	}

	// All outgoing packets must use device.Send.
	return dev.Send(pkt)
}

func (p *ClipboardPlugin) readClipboard() string {
	var cmd *exec.Cmd
	switch backend, _ := p.getBackend(); backend {
	case backendWayland:
		cmd = p.clipboardCmd(context.Background(), "wl-paste", "-n")
	case backendX11:
		cmd = p.clipboardCmd(context.Background(), "xclip", "-selection", "clipboard", "-o")
	default:
		return ""
	}
	out, err := cmd.Output()
	if err != nil {
		p.logger.Debug("clipboard: read failed", zap.Error(err))
		return ""
	}
	return string(out)
}

func (p *ClipboardPlugin) OnConnect(dev device.Sender) {
	if !p.pushOnConnect {
		return
	}
	content := p.readClipboard()

	p.mu.Lock()
	p.lastPushedContent = content
	p.mu.Unlock()

	body := ClipboardBody{
		Content:   content,
		Timestamp: time.Now().UnixMilli(),
	}
	pkt, err := protocol.NewPacket("kdeconnect.clipboard.connect", body)
	if err != nil {
		p.logger.Debug("clipboard: OnConnect: failed to build packet", zap.Error(err))
		return
	}
	// Best-effort — device may still be completing the TLS handshake.
	_ = dev.Send(pkt)
}

func (p *ClipboardPlugin) OnDisconnect(dev device.Sender) {
}
