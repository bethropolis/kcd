package sms

import (
	"context"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
)

// SMSPlugin implements the basic SMS sending capability of KDE Connect.
type SMSPlugin struct{}

// NewSMSPlugin creates a new SMS plugin.
func NewSMSPlugin() *SMSPlugin {
	return &SMSPlugin{}
}

func (p *SMSPlugin) Name() string            { return "SMS" }
func (p *SMSPlugin) Timeout() time.Duration  { return 5 * time.Second }
func (p *SMSPlugin) IncomingTypes() []string { return []string{} } // Receive not implemented yet
func (p *SMSPlugin) OutgoingTypes() []string { return []string{"kdeconnect.sms.request"} }

func (p *SMSPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	return nil
}

// SendSMS asks the remote device to send an SMS.
func (p *SMSPlugin) SendSMS(dev device.Sender, phoneNumber, message string) error {
	pkt, err := protocol.NewPacket("kdeconnect.sms.request", map[string]interface{}{
		"sendSms":     true,
		"phoneNumber": phoneNumber,
		"messageBody": message,
	})
	if err != nil {
		return err
	}
	// Packets must always be sent via the device's Send method.
	return dev.Send(pkt)
}

func (p *SMSPlugin) OnConnect(dev device.Sender)    {}
func (p *SMSPlugin) OnDisconnect(dev device.Sender) {}
