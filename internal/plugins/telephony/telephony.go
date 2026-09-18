package telephony

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

type TelephonyPlugin struct {
	notifications config.NotificationConfig
	bus           *events.Bus
	logger        *zap.Logger
}

func NewTelephonyPlugin(bus *events.Bus, logger *zap.Logger, notifications ...config.NotificationConfig) *TelephonyPlugin {
	var notificationCfg config.NotificationConfig
	if len(notifications) > 0 {
		notificationCfg = notifications[0]
	}
	return &TelephonyPlugin{
		notifications: notificationCfg,
		bus:           bus,
		logger:        logger.With(zap.String("plugin", "telephony")),
	}
}

type TelephonyBody struct {
	Event       string            `json:"event"` // "ringing", "talking", "missedCall"
	ContactName string            `json:"contactName"`
	PhoneNumber string            `json:"phoneNumber"`
	IsCancel    protocol.FlexBool `json:"isCancel"`
}

func (p *TelephonyPlugin) Name() string            { return "Telephony" }
func (p *TelephonyPlugin) Timeout() time.Duration  { return 5 * time.Second }
func (p *TelephonyPlugin) IncomingTypes() []string { return []string{"kdeconnect.telephony"} }
func (p *TelephonyPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.telephony.request_mute"}
}

func (p *TelephonyPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body TelephonyBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	canceled := bool(body.IsCancel)
	if p.bus != nil {
		if canceled {
			p.bus.Publish(events.TypeTelephonyCanceled, dev.ID(), body)
		} else {
			eventType := events.EventType("telephony." + body.Event)
			if body.Event == "missedCall" {
				eventType = events.TypeTelephonyMissed
			}
			p.bus.Publish(eventType, dev.ID(), body)
		}
	}

	go func() {
		if canceled {
			return
		}

		var title, message, urgency string
		urgency = "normal"
		caller := body.ContactName
		if caller == "" {
			caller = body.PhoneNumber
		}

		switch body.Event {
		case "ringing":
			title = "📞 Incoming Call"
			message = "Ringing: " + caller
			urgency = "critical"
		case "missed", "missedCall":
			title = "❌ Missed Call"
			message = "Missed call from " + caller
		default:
			return
		}

		plugin.RunCommandAsync(p.logger, "notify-send", "-a", p.notifications.AppName(), "-u", urgency, title, message)
	}()

	return nil
}

func (p *TelephonyPlugin) Mute(dev device.Sender) error {
	pkt, err := protocol.NewPacket("kdeconnect.telephony.request_mute", map[string]string{"action": "mute"})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

func (p *TelephonyPlugin) OnConnect(dev device.Sender)    {}
func (p *TelephonyPlugin) OnDisconnect(dev device.Sender) {}
