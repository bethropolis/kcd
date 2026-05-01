package ping

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

type PingPlugin struct {
	cfg    config.PingConfig
	bus    *events.Bus
	logger *zap.Logger
}

func NewPingPlugin(cfg config.PingConfig, bus *events.Bus, logger *zap.Logger) *PingPlugin {
	return &PingPlugin{
		cfg:    cfg,
		bus:    bus,
		logger: logger.With(zap.String("plugin", "ping")),
	}
}

type PingBody struct {
	Message string `json:"message"`
}

func (p *PingPlugin) Name() string            { return "Ping" }
func (p *PingPlugin) Timeout() time.Duration  { return 5 * time.Second }
func (p *PingPlugin) IncomingTypes() []string { return []string{"kdeconnect.ping"} }
func (p *PingPlugin) OutgoingTypes() []string { return []string{"kdeconnect.ping"} }

func (p *PingPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
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
	plugin.RunCommandAsync(p.logger, "notify-send",
		"-a", p.cfg.AppName,
		"-i", p.cfg.Icon,
		"Ping from "+dev.Name(), msg,
	)

	return nil
}

func (p *PingPlugin) OnConnect(dev device.Sender)    {}
func (p *PingPlugin) OnDisconnect(dev device.Sender) {}
