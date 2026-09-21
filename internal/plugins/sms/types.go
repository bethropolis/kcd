package sms

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
)

const (
	PacketTypeSMSMessages       = "kdeconnect.sms.messages"
	PacketTypeSMSRequest        = "kdeconnect.sms.request"
	PacketTypeSMSRequestConvs   = "kdeconnect.sms.request_conversations"
	PacketTypeSMSRequestConv    = "kdeconnect.sms.request_conversation"
	PacketTypeSMSRequestAtt     = "kdeconnect.sms.request_attachment"
	PacketTypeSMSAttachmentFile = "kdeconnect.sms.attachment_file"

	maxSMSMessages = 1000 // safety limit to prevent OOM from malicious payload

	// maxSMSAttachmentBytes caps a single MMS attachment download (mirrors
	// the clipboard 50MB safety limit). Anything larger is refused before
	// any bytes hit disk.
	maxSMSAttachmentBytes = 50 * 1024 * 1024
)

// SMSPlugin implements SMS sending, receiving, conversation browsing, and MMS
// attachment handling for KDE Connect.
type SMSPlugin struct {
	sidechannel   transport.SidechannelOptions
	notifications config.NotificationConfig
	cfg           config.SMSConfig
	bus           *events.Bus
	tlsConfig     *tls.Config
	logger        log.Logger
	cacheDir      string
}

// Options customizes storage, network timeouts, and desktop notification identity.
type Options struct {
	CacheDir      string
	Sidechannel   transport.SidechannelOptions
	Notifications config.NotificationConfig
}

func NewSMSPlugin(cfg config.SMSConfig, bus *events.Bus, tlsConfig *tls.Config, logger log.Logger, options ...Options) *SMSPlugin {
	var opts Options
	if len(options) > 0 {
		opts = options[0]
	}
	cacheDir := opts.CacheDir
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "kcd", "sms-attachments")
	}
	_ = os.MkdirAll(cacheDir, 0700)

	return &SMSPlugin{
		sidechannel:   opts.Sidechannel,
		notifications: opts.Notifications,
		cfg:           cfg,
		bus:           bus,
		tlsConfig:     tlsConfig,
		logger:        logger.With(log.String("plugin", "sms")),
		cacheDir:      cacheDir,
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
	Event       int               `json:"event"`
	Body        string            `json:"body"`
	Addresses   []SMSAddress      `json:"addresses"`
	Date        int64             `json:"date"`
	Type        int               `json:"type"`
	ThreadID    int64             `json:"thread_id"`
	Read        protocol.FlexBool `json:"read"`
	UID         int64             `json:"u_id,omitempty"`
	SubID       int               `json:"sub_id,omitempty"`
	Attachments []SMSAttachment   `json:"attachments,omitempty"`
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
