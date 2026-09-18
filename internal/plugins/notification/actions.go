package notification

import (
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
)

// RequestReply sends a reply back to an Android notification.
func (p *NotificationPlugin) RequestReply(dev device.Sender, replyID, message string) error {
	pkt, err := protocol.NewPacket("kdeconnect.notification.reply", map[string]string{
		"requestReplyId": replyID,
		"message":        message,
	})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

// Dismiss asks the phone to clear the notification with the given ID and
// closes the matching desktop popup, if one is tracked.
func (p *NotificationPlugin) Dismiss(dev device.Sender, id string) error {
	pkt, err := protocol.NewPacket("kdeconnect.notification.request", map[string]string{
		"cancel": id,
	})
	if err != nil {
		return err
	}
	if err := dev.Send(pkt); err != nil {
		return err
	}
	if id != "" {
		if desktopID, ok := p.notifIDs.LoadAndDelete(p.notifKey(dev.ID(), id)); ok {
			if s, ok := desktopID.(string); ok {
				p.closeNotification(s)
			}
		}
	}
	return nil
}

func (p *NotificationPlugin) OnConnect(_ device.Sender)    {}
func (p *NotificationPlugin) OnDisconnect(_ device.Sender) {}
