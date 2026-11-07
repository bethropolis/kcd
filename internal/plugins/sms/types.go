package sms

import (
	"crypto/tls"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
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

	// maxSMSAttachmentBytes caps a single MMS download (mirroring clipboard's
	// limit); anything larger is refused before any bytes hit disk.
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

	// mu guards devices and armedAt.
	mu sync.RWMutex
	// devices holds the currently connected devices, keyed by ID, so the
	// bus hook can arm them without reaching for a registry.
	devices map[string]device.Peer
	// armedAt records per-device arm times, doubling as the armed set: arming
	// twice would re-trigger a full history burst for no benefit.
	armedAt map[string]time.Time

	// attachmentThreads maps uniqueIdentifier -> threadID for requested attachments.
	attachmentThreads sync.Map
}

// Options customizes storage, network timeouts, and desktop notification identity.
type Options struct {
	CacheDir      string
	Sidechannel   transport.SidechannelOptions
	Notifications config.NotificationConfig
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
	UID         int64             `json:"_id,omitempty"` // Telephony.Sms._ID; published as u_id
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
