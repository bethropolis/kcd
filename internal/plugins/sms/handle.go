package sms

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
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

		if p.cfg.NotifyIncoming {
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
