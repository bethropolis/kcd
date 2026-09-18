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

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
	"go.uber.org/zap"
)

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
		switch backend, _ := p.getBackend(); backend {
		case backendWayland:
			// -n: wl-copy appends a trailing newline by default. Without it the
			// local selection becomes content+"\n", which differs from the
			// inbound lastContent guard and makes --watch echo the phone's own
			// clipboard straight back to it.
			if err := p.runCopy(context.Background(), p.clipboardCmd("wl-copy", "-n"), strings.NewReader(body.Content)); err != nil {
				p.logger.Warn("clipboard: failed to set clipboard", zap.Error(err))
			}
		case backendX11:
			if err := p.runCopy(context.Background(), p.clipboardCmd("xclip", "-selection", "clipboard"), strings.NewReader(body.Content)); err != nil {
				p.logger.Warn("clipboard: failed to set clipboard", zap.Error(err))
			}
		default:
			p.logger.Debug("clipboard: no backend available, dropping inbound copy")
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
	expectedFP := cert.PinnedFingerprint(dev.PeerCert())

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

		if err := downloadToFile(ctx, remoteIP, payloadPort, payloadSize, tmpPath, p.tlsConfig, expectedFP, p.logger, p.sidechannel); err != nil {
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
			cmd = p.clipboardCmd("wl-copy", "-n", "--type", mimeType)
		case backendX11:
			cmd = p.clipboardCmd("xclip", "-selection", "clipboard", "-t", mimeType, "-i")
		default:
			return
		}
		if err := p.runCopy(context.Background(), cmd, t); err != nil {
			p.logger.Warn("clipboard file: failed to set clipboard", zap.Error(err))
		}
	}()

	return nil
}

// downloadToFile dials a TLS side-channel and streams the payload to dest.
func downloadToFile(ctx context.Context, ip net.IP, port int, size int64, dest string, tlsConfig *tls.Config, expectedFP string, logger *zap.Logger, options ...transport.SidechannelOptions) error {
	conn, err := transport.DialSidechannel(ctx, ip, port, tlsConfig, expectedFP, logger, options...)
	if err != nil {
		return err
	}
	defer conn.Close()

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("clipboard: create file %s: %w", dest, err)
	}
	defer f.Close()

	_, err = io.Copy(f, io.LimitReader(conn, size))
	if err != nil {
		os.Remove(dest) // don't leave a corrupt partial behind
		return fmt.Errorf("clipboard: stream to %s: %w", dest, err)
	}
	return nil
}
