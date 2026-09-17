package client

import (
	"encoding/json"
	"fmt"

	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugins/contacts"
)

// SendSMS requests the remote device to send an SMS.
func (c *Client) SendSMS(deviceID, phoneNumber, message string) error {
	_, err := c.Call(ipc.CmdSendSMS, ipc.SMSPayload{
		DeviceID:    deviceID,
		PhoneNumber: phoneNumber,
		Message:     message,
	})
	return err
}

// SmsRequestConversations asks a device to send a list of all SMS conversations.
func (c *Client) SmsRequestConversations(deviceID string) error {
	_, err := c.Call(ipc.CmdSmsRequestConvs, ipc.DevicePayload{DeviceID: deviceID})
	return err
}

// SmsRequestConversation asks a device to send messages from a specific thread.
func (c *Client) SmsRequestConversation(deviceID string, threadID int64) error {
	_, err := c.Call(ipc.CmdSmsRequestConv, ipc.SMSConvPayload{DeviceID: deviceID, ThreadID: threadID})
	return err
}

// SmsRequestAttachment asks a device to send an MMS attachment file.
func (c *Client) SmsRequestAttachment(deviceID string, partID int64, uniqueIdentifier string) error {
	_, err := c.Call(ipc.CmdSmsRequestAttachment, ipc.SMSAttachmentPayload{
		DeviceID:         deviceID,
		PartID:           partID,
		UniqueIdentifier: uniqueIdentifier,
	})
	return err
}

// ContactsSync asks the daemon to start a contacts sync round with a device.
// Results arrive async via the contacts.updated event.
func (c *Client) ContactsSync(deviceID string) error {
	_, err := c.Call(ipc.CmdContactsSync, ipc.DevicePayload{DeviceID: deviceID})
	return err
}

// ContactsList returns cached contact summaries for a device (empty when
// never synced — absent means unknown).
func (c *Client) ContactsList(deviceID string) ([]contacts.ContactSummary, error) {
	res, err := c.Call(ipc.CmdContactsList, ipc.DevicePayload{DeviceID: deviceID})
	if err != nil {
		return nil, err
	}
	var list []contacts.ContactSummary
	if err := json.Unmarshal(res.Data, &list); err != nil {
		return nil, fmt.Errorf("decode contacts: %w", err)
	}
	return list, nil
}
