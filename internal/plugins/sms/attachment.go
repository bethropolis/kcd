package sms

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
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
		dlCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		if err := p.receiveAttachment(dlCtx, remoteIP, port, payloadSize, destPath, expectedFP); err != nil {
			p.logger.Error("sms: attachment download failed", log.Error(err))
			return
		}
		p.logger.Info("sms: attachment downloaded",
			log.String("path", destPath),
			log.String("filename", body.Filename),
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

// receiveAttachment downloads the file over TLS, capped at the declared
// payload size (itself bounded) so a malicious peer can't fill the disk.
func (p *SMSPlugin) receiveAttachment(ctx context.Context, ip net.IP, port int, size int64, destPath string, expectedFP string) error {
	if size <= 0 || size > maxSMSAttachmentBytes {
		return fmt.Errorf("sms: refusing attachment with invalid size %d (limit %d)", size, maxSMSAttachmentBytes)
	}
	_, err := transport.ReceiveToFile(ctx, ip, port, size, destPath, p.tlsConfig, expectedFP, p.logger, transport.ReceiveOptions{MaxSize: maxSMSAttachmentBytes, Sidechannel: p.sidechannel})
	if err != nil {
		return fmt.Errorf("sms: receive attachment data: %w", err)
	}

	return nil
}

// maxFilenameLength caps attachment filenames to keep them manageable.
const maxFilenameLength = 128

// cleanFilename hardens attachment paths: normalize backslashes, drop
// controls, strip components (traversal), truncate overlong names.
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
