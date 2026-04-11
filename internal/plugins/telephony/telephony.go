package telephony

import (
	"context"
	"encoding/json"
	"os/exec"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/dbusutil"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/godbus/dbus/v5"
	"go.uber.org/zap"
)

type TelephonyPlugin struct {
	bus        *events.Bus
	logger     *zap.Logger
	pauseMusic bool

	mu           sync.Mutex
	dbusConn     *dbus.Conn
	pausedPlayers []string
}

func NewTelephonyPlugin(bus *events.Bus) *TelephonyPlugin {
	return &TelephonyPlugin{bus: bus}
}

func NewTelephonyPluginWithOptions(bus *events.Bus, pauseMusic bool, logger *zap.Logger) *TelephonyPlugin {
	p := &TelephonyPlugin{bus: bus, pauseMusic: pauseMusic, logger: logger.With(zap.String("plugin", "telephony"))}
	if pauseMusic {
		conn, err := dbus.SessionBus()
		if err != nil {
			p.logger.Warn("telephony: failed to connect to session D-Bus for pause music", zap.Error(err))
		} else {
			p.dbusConn = conn
		}
	}
	return p
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
func (p *TelephonyPlugin) OutgoingTypes() []string { return []string{"kdeconnect.telephony.request_mute"} }

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
		// Pause Music integration: pause on call start, resume on cancel.
		if p.pauseMusic && p.dbusConn != nil {
			if body.IsCancel {
				p.mu.Lock()
				paused := p.pausedPlayers
				p.pausedPlayers = nil
				p.mu.Unlock()
				if len(paused) > 0 {
					dbusutil.PlayMPRIS(p.dbusConn, paused, p.logger)
				}
			} else if body.Event == "ringing" || body.Event == "talking" {
				p.mu.Lock()
				if len(p.pausedPlayers) == 0 {
					p.pausedPlayers = dbusutil.PauseMPRIS(p.dbusConn, p.logger)
				}
				p.mu.Unlock()
			}
		}

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
			return // ignore "talking" or unknown events for notifications
		}

		_ = exec.Command("notify-send", "-a", "KDE Connect", "-u", urgency, title, message).Run()
	}()

	return nil
}

func (p *TelephonyPlugin) OnConnect(dev device.Sender)    {}
func (p *TelephonyPlugin) OnDisconnect(dev device.Sender) {}

// Mute sends a mute request to the remote device to silence an incoming call.
func (p *TelephonyPlugin) Mute(dev device.Sender) error {
	pkt, err := protocol.NewPacket("kdeconnect.telephony.request_mute", map[string]string{"action": "mute"})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}
