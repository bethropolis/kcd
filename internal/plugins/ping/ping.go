package ping

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/sysexec"
)

var _ plugin.Plugin = (*PingPlugin)(nil)

type PingPlugin struct {
	cfg    config.PingConfig
	bus    *events.Bus
	logger log.Logger
}

func NewPingPlugin(cfg config.PingConfig, bus *events.Bus, logger log.Logger, notifications ...config.NotificationConfig) *PingPlugin {
	if cfg.AppName == "" {
		var notificationCfg config.NotificationConfig
		if len(notifications) > 0 {
			notificationCfg = notifications[0]
		}
		cfg.AppName = notificationCfg.AppName()
	}
	return &PingPlugin{
		cfg:    cfg,
		bus:    bus,
		logger: logger.With(log.String("plugin", "ping")),
	}
}

type PingBody struct {
	Message string `json:"message"`
}

func (p *PingPlugin) Name() string            { return "Ping" }
func (p *PingPlugin) Timeout() time.Duration  { return 5 * time.Second }
func (p *PingPlugin) IncomingTypes() []string { return []string{"kdeconnect.ping"} }
func (p *PingPlugin) OutgoingTypes() []string { return []string{"kdeconnect.ping"} }

func (p *PingPlugin) Handle(ctx context.Context, dev device.Peer, pkt *protocol.Packet) error {
	var body PingBody
	_ = json.Unmarshal(pkt.Body, &body)

	msg := body.Message
	if msg == "" {
		msg = p.cfg.DefaultMessage
		if msg == "" {
			msg = "Ping!"
		}
	}

	if p.bus != nil {
		p.bus.Publish(events.TypePingReceived, dev.ID(), map[string]string{
			"message": msg,
		})
	}

	// Do not block - spawn goroutine
	sysexec.RunAsync(p.logger, "notify-send",
		"-a", p.cfg.AppName,
		"-i", p.cfg.Icon,
		"Ping from "+dev.Name(), msg,
	)

	return nil
}

func (p *PingPlugin) OnConnect(dev device.Peer)    {}
func (p *PingPlugin) OnDisconnect(dev device.Peer) {}

// Ping sends a ping packet to the device.
func (p *PingPlugin) Ping(dev device.Peer, message ...string) error {
	msg := "Ping!"
	if len(message) > 0 && message[0] != "" {
		msg = message[0]
	} else if p.cfg.DefaultMessage != "" {
		msg = p.cfg.DefaultMessage
	}
	pkt, err := protocol.NewPacket("kdeconnect.ping", map[string]string{"message": msg})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}
