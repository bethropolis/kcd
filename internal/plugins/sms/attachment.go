package sms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
	"go.uber.org/zap"
)

// handleAttachmentFile downloads an MMS attachment file sent by the phone.
func (p *SMSPlugin) handleAttachmentFile(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	if pkt.Body == nil {
		return nil
	}

	var body AttachmentFileBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return fmt.Errorf("sms: unmarshal attachment body: %w", err)
	}

	if body.Filename == "" {
		return nil
	}

	if pkt.PayloadTransferInfo == nil || pkt.PayloadTransferInfo.Port == 0 {
		p.logger.Warn("sms: attachment file received without side-channel transfer info (inline payload not supported)")
		return nil
	}

	cleanName := cleanFilename(body.Filename)
	destPath := filepath.Join(p.cacheDir, cleanName)

	remoteIP := dev.RemoteIP()
	if remoteIP == nil {
		return fmt.Errorf("sms: failed to resolve remote peer IP")
	}

	port := pkt.PayloadTransferInfo.Port
	payloadSize := pkt.PayloadSize
	expectedFP := cert.PinnedFingerprint(dev.PeerCert())

	go func() {
		if err := p.receiveAttachment(ctx, remoteIP, port, payloadSize, destPath, expectedFP); err != nil {
			p.logger.Error("sms: attachment download failed", zap.Error(err))
			return
		}
		p.logger.Info("sms: attachment downloaded",
			zap.String("path", destPath),
			zap.String("filename", body.Filename),
		)
		if p.bus != nil {
			p.bus.Publish(events.TypeSMSAttachment, dev.ID(), map[string]any{
				"filename":  body.Filename,
				"path":      destPath,
				"thread_id": body.ThreadID,
			})
		}
	}()

	return nil
}

// receiveAttachment connects to the phone's side-channel port and downloads
// the attachment file over TLS. The stream is capped at the declared
// payload size (itself bounded by maxSMSAttachmentBytes) so a malicious
// peer can't fill the disk with an unbounded stream.
func (p *SMSPlugin) receiveAttachment(ctx context.Context, ip net.IP, port int, size int64, destPath string, expectedFP string) error {
	if size <= 0 || size > maxSMSAttachmentBytes {
		return fmt.Errorf("sms: refusing attachment with invalid size %d (limit %d)", size, maxSMSAttachmentBytes)
	}
	conn, err := transport.DialSidechannel(ctx, ip, port, p.tlsConfig, expectedFP, p.logger, p.sidechannel)
	if err != nil {
		return fmt.Errorf("sms: connect to attachment side-channel: %w", err)
	}
	defer conn.Close()

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("sms: create attachment file: %w", err)
	}
	defer f.Close()

	_, err = io.Copy(f, io.LimitReader(conn, size))
	if err != nil {
		os.Remove(destPath) // don't leave a corrupt partial behind
		return fmt.Errorf("sms: receive attachment data: %w", err)
	}

	return nil
}

// maxFilenameLength caps attachment filenames to keep them manageable.
const maxFilenameLength = 128

// cleanFilename strips path components to prevent directory traversal in
// attachment file paths. Backslashes are normalized first (Windows-style
// paths), control characters dropped, and overlong names truncated.
func cleanFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if len(name) > maxFilenameLength {
		name = strings.ToValidUTF8(name[:maxFilenameLength], "")
	}
	if name == "." || name == ".." || name == "/" || name == "" {
		return "downloaded_attachment"
	}
	return name
}
