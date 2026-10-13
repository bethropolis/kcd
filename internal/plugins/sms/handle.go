package sms

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
)

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

	// The phone's content observer fires on any SMS database change and has
	// no empty guard of its own, so empty batches are routine once armed.
	if len(batch.Messages) == 0 {
		return nil
	}

	if len(batch.Messages) > maxSMSMessages {
		return fmt.Errorf("sms: messages batch too large: %d (max %d)", len(batch.Messages), maxSMSMessages)
	}

	// Resolved once per batch: the arm time and whether we are still
	// wanted. See shouldNotify for why both matter.
	armAt, isArmed := p.armedAtFor(dev.ID())
	armAtMs := armAt.UnixMilli()
	stillArmed := p.armed()

	for _, msg := range batch.Messages {
		if msg.Body == "" {
			continue
		}

		msg := msg // capture

		sender := ""
		if len(msg.Addresses) > 0 {
			sender = msg.Addresses[0].Address
		}

		// Never log the body: journals are routinely collected and shipped
		// off-box, and a message is the most sensitive thing we handle.
		p.logger.Debug("sms: message received",
			log.String("from", sender),
			log.Int64("thread_id", msg.ThreadID),
		)

		// Reaching here means a subscriber named sms.incoming, since an
		// unfiltered watch stream excludes it. That naming is the consent.
		if p.bus != nil {
			payload := map[string]any{
				"body":      msg.Body,
				"sender":    sender,
				"date":      msg.Date,
				"type":      msg.Type,
				"thread_id": msg.ThreadID,
				"read":      bool(msg.Read),
				"event":     msg.Event,
				"u_id":      msg.UID,
				"sub_id":    msg.SubID,
			}
			if len(msg.Attachments) > 0 {
				payload["attachments"] = msg.Attachments
			}
			p.bus.Publish(events.TypeSMSIncoming, dev.ID(), payload)
		}

		// Type 1 is an inbound message; type 2 is one we sent, and the
		// phone echoes those back in the same batch.
		if p.shouldNotify(msg, isArmed && stillArmed, armAtMs) {
			msgText := msg.Body
			if len(msgText) > 120 {
				msgText = msgText[:120] + "…"
			}
			title := fmt.Sprintf("SMS from %s", sender)
			plugin.RunCommandAsync(p.logger, "notify-send",
				"-a", p.notifications.AppName(),
				"-i", "dialog-information",
				title,
				msgText,
			)
		}
	}

	return nil
}

// shouldNotify drops the arming-request's history burst and stays quiet
// without subscribers: the phone cannot be un-armed and pushes for life.
func (p *SMSPlugin) shouldNotify(msg SMSMessage, isArmed bool, armAtMs int64) bool {
	if !p.cfg.NotifyIncoming || !isArmed {
		return false
	}
	// Type 1 is inbound; type 2 is a message we sent, echoed back in the
	// same batch.
	if msg.Type != 1 {
		return false
	}
	return msg.Date >= armAtMs
}

// armedAtFor returns when the device was armed and whether it is armed at
// all. Messages older than the arm time are the arming burst, not new SMS.
func (p *SMSPlugin) armedAtFor(deviceID string) (time.Time, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	t, ok := p.armedAt[deviceID]
	return t, ok
}
