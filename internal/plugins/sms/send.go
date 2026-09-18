package sms

import (
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
)

// --- SMS sending -----------------------------------------------------------

func (p *SMSPlugin) SendSMS(dev device.Sender, phoneNumber, message string) error {
	// v2 schema: the phone reads only messageBody, with addresses as the
	// primary recipient list (phoneNumber stays as a legacy fallback for
	// older peers). Without addresses/version the phone sends a blank SMS.
	body := map[string]any{
		"version":     2,
		"addresses":   []map[string]string{{"address": phoneNumber}},
		"messageBody": message,
		"phoneNumber": phoneNumber,
	}
	pkt, err := protocol.NewPacket(PacketTypeSMSRequest, body)
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

// --- Conversation browsing (Phase 2) ---------------------------------------

// RequestConversations asks the phone for a summary of all conversations.
// Bodyless requests use an empty object (never null) on the wire; see
// contacts.RequestSync for why explicit null is dangerous.
func (p *SMSPlugin) RequestConversations(dev device.Sender) error {
	pkt, err := protocol.NewPacket(PacketTypeSMSRequestConvs, map[string]any{})
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

// --- Lifecycle -------------------------------------------------------------

func (p *SMSPlugin) OnConnect(dev device.Sender)    {}
func (p *SMSPlugin) OnDisconnect(dev device.Sender) {}
