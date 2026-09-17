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

func (p *NotificationPlugin) OnConnect(_ device.Sender)    {}
func (p *NotificationPlugin) OnDisconnect(_ device.Sender) {}
