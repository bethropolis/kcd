package sms

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

const (
	PacketTypeSMSMessages       = "kdeconnect.sms.messages"
	PacketTypeSMSRequest        = "kdeconnect.sms.request"
	PacketTypeSMSRequestConvs   = "kdeconnect.sms.request_conversations"
	PacketTypeSMSRequestConv    = "kdeconnect.sms.request_conversation"
	PacketTypeSMSRequestAtt     = "kdeconnect.sms.request_attachment"
	PacketTypeSMSAttachmentFile = "kdeconnect.sms.attachment_file"

	maxSMSMessages = 1000 // safety limit to prevent OOM from malicious payload
)

// SMSPlugin implements SMS sending, receiving, conversation browsing, and MMS
// attachment handling for KDE Connect.
type SMSPlugin struct {
	cfg       config.SMSConfig
	bus       *events.Bus
	tlsConfig *tls.Config
	logger    *zap.Logger
	cacheDir  string
}

func NewSMSPlugin(cfg config.SMSConfig, bus *events.Bus, tlsConfig *tls.Config, logger *zap.Logger) *SMSPlugin {
	cacheDir := filepath.Join(os.TempDir(), "kcd", "sms-attachments")
	_ = os.MkdirAll(cacheDir, 0700)

	return &SMSPlugin{
		cfg:       cfg,
		bus:       bus,
		tlsConfig: tlsConfig,
		logger:    logger.With(zap.String("plugin", "sms")),
		cacheDir:  cacheDir,
	}
}

func (p *SMSPlugin) Name() string           { return "SMS" }
func (p *SMSPlugin) Timeout() time.Duration { return 5 * time.Second }

func (p *SMSPlugin) IncomingTypes() []string {
	return []string{PacketTypeSMSMessages, PacketTypeSMSAttachmentFile}
}

func (p *SMSPlugin) OutgoingTypes() []string {
	return []string{
		PacketTypeSMSRequest,
		PacketTypeSMSRequestConvs,
		PacketTypeSMSRequestConv,
		PacketTypeSMSRequestAtt,
	}
}

// --- Packet body types -------------------------------------------------------

type SMSMessagesPacket struct {
	Version  int          `json:"version"`
	Messages []SMSMessage `json:"messages"`
}

type SMSMessage struct {
	Event       int             `json:"event"`
	Body        string          `json:"body"`
	Addresses   []SMSAddress    `json:"addresses"`
	Date        int64           `json:"date"`
	Type        int             `json:"type"`
	ThreadID    int64           `json:"thread_id"`
	Read        bool            `json:"read"`
	UID         int64           `json:"u_id,omitempty"`
	SubID       int             `json:"sub_id,omitempty"`
	Attachments []SMSAttachment `json:"attachments,omitempty"`
}

type SMSAddress struct {
	Address string `json:"address"`
}

type SMSAttachment struct {
	PartID           int64  `json:"part_id"`
	MimeType         string `json:"mime_type"`
	EncodedThumbnail string `json:"encoded_thumbnail,omitempty"`
	UniqueIdentifier string `json:"unique_identifier"`
}

type AttachmentFileBody struct {
	Filename string `json:"filename"`
	ThreadID int64  `json:"thread_id,omitempty"`
}

// --- Handle ----------------------------------------------------------------

func (p *SMSPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	switch pkt.Type {
	case PacketTypeSMSMessages:
		return p.handleMessages(ctx, dev, pkt)
	case PacketTypeSMSAttachmentFile:
		return p.handleAttachmentFile(ctx, dev, pkt)
	}
	return nil
}

// handleMessages parses a batch of SMS messages from the phone and publishes
// one event per message.
func (p *SMSPlugin) handleMessages(_ context.Context, dev device.Sender, pkt *protocol.Packet) error {
	if pkt.Body == nil {
		return nil
	}

	var batch SMSMessagesPacket
	if err := json.Unmarshal(pkt.Body, &batch); err != nil {
		return fmt.Errorf("sms: unmarshal messages batch: %w", err)
	}

	if len(batch.Messages) > maxSMSMessages {
		return fmt.Errorf("sms: messages batch too large: %d (max %d)", len(batch.Messages), maxSMSMessages)
	}

	for _, msg := range batch.Messages {
		if msg.Body == "" {
			continue
		}

		msg := msg // capture

		sender := ""
		if len(msg.Addresses) > 0 {
			sender = msg.Addresses[0].Address
		}

		p.logger.Debug("sms: message received",
			zap.String("from", sender),
			zap.String("body", msg.Body),
			zap.Int64("thread_id", msg.ThreadID),
		)

		if p.bus != nil {
			payload := map[string]any{
				"body":      msg.Body,
				"sender":    sender,
				"date":      msg.Date,
				"type":      msg.Type,
				"thread_id": msg.ThreadID,
				"read":      msg.Read,
				"event":     msg.Event,
				"u_id":      msg.UID,
				"sub_id":    msg.SubID,
			}
			if len(msg.Attachments) > 0 {
				payload["attachments"] = msg.Attachments
			}
			p.bus.Publish(events.TypeSMSIncoming, dev.ID(), payload)
		}

		if p.cfg.NotifyIncoming {
			msgText := msg.Body
			if len(msgText) > 120 {
				msgText = msgText[:120] + "…"
			}
			title := fmt.Sprintf("SMS from %s", sender)
			plugin.RunCommandAsync(p.logger, "notify-send",
				"-a", "KDE Connect",
				"-i", "dialog-information",
				title,
				msgText,
			)
		}
	}

	return nil
}

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

	go func() {
		if err := p.receiveAttachment(ctx, remoteIP, port, payloadSize, destPath); err != nil {
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
// the attachment file over TLS.
func (p *SMSPlugin) receiveAttachment(ctx context.Context, ip net.IP, port int, _ int64, destPath string) error {
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(port))

	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		},
		Config: p.tlsConfig,
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("sms: connect to attachment side-channel: %w", err)
	}
	defer conn.Close()

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("sms: create attachment file: %w", err)
	}
	defer f.Close()

	_, err = io.Copy(f, conn)
	if err != nil {
		return fmt.Errorf("sms: receive attachment data: %w", err)
	}

	return nil
}

// --- SMS sending -----------------------------------------------------------

func (p *SMSPlugin) SendSMS(dev device.Sender, phoneNumber, message string) error {
	body := map[string]any{
		"sendSms":     true,
		"phoneNumber": phoneNumber,
		"messageBody": message,
	}
	pkt, err := protocol.NewPacket(PacketTypeSMSRequest, body)
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

// --- Conversation browsing (Phase 2) ---------------------------------------

// RequestConversations asks the phone for a summary of all conversations.
func (p *SMSPlugin) RequestConversations(dev device.Sender) error {
	pkt, err := protocol.NewPacket(PacketTypeSMSRequestConvs, nil)
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

// RequestConversation asks the phone for messages in a specific thread.
// Pass -1 for rangeStartTimestamp or numberToRequest for no limit.
func (p *SMSPlugin) RequestConversation(dev device.Sender, threadID int64, rangeStartTimestamp int64, numberToRequest int64) error {
	body := map[string]any{
		"threadID": threadID,
	}
	if rangeStartTimestamp >= 0 {
		body["rangeStartTimestamp"] = rangeStartTimestamp
	}
	if numberToRequest >= 0 {
		body["numberToRequest"] = numberToRequest
	}
	pkt, err := protocol.NewPacket(PacketTypeSMSRequestConv, body)
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

// RequestAttachment asks the phone to send an MMS attachment file.
func (p *SMSPlugin) RequestAttachment(dev device.Sender, partID int64, uniqueIdentifier string) error {
	body := map[string]any{
		"part_id":           partID,
		"unique_identifier": uniqueIdentifier,
	}
	pkt, err := protocol.NewPacket(PacketTypeSMSRequestAtt, body)
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

// cleanFilename strips path components to prevent directory traversal in
// attachment file paths.
func cleanFilename(name string) string {
	name = filepath.Base(name)
	if name == "." || name == ".." || name == "" {
		return "downloaded_attachment"
	}
	return name
}

// --- Lifecycle -------------------------------------------------------------

func (p *SMSPlugin) OnConnect(dev device.Sender)    {}
func (p *SMSPlugin) OnDisconnect(dev device.Sender) {}
