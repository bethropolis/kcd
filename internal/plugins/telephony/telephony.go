package telephony

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

type TelephonyPlugin struct {
	bus    *events.Bus
	logger *zap.Logger
}

func NewTelephonyPlugin(bus *events.Bus, logger *zap.Logger) *TelephonyPlugin {
	return &TelephonyPlugin{
		bus:    bus,
		logger: logger.With(zap.String("plugin", "telephony")),
	}
}

type TelephonyBody struct {
	Event       string `json:"event"` // "ringing", "talking", "missed"
	ContactName string `json:"contactName"`
	PhoneNumber string `json:"phoneNumber"`
	IsCancel    bool   `json:"isCancel"`
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

	if p.bus != nil {
		if body.IsCancel {
			p.bus.Publish(events.TypeTelephonyCanceled, dev.ID(), body)
		} else {
			p.bus.Publish(events.EventType("telephony."+body.Event), dev.ID(), body)
		}
	}

	go func() {
		if body.IsCancel {
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
		case "missed":
			title = "❌ Missed Call"
			message = "Missed call from " + caller
		default:
			return
		}

		plugin.RunCommandAsync(p.logger, "notify-send", "-a", "KDE Connect", "-u", urgency, title, message)
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
