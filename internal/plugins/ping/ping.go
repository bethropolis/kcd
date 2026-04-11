package ping

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
)

type PingPlugin struct {
	bus *events.Bus
}

func NewPingPlugin(bus *events.Bus) *PingPlugin {
	return &PingPlugin{bus: bus}
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
		msg = "Ping!"
	}

	if p.bus != nil {
		p.bus.Publish(events.TypePingReceived, dev.ID(), map[string]string{
			"message": msg,
		})
	}

	// Do not block - spawn goroutine
	go func() {
		_ = exec.Command("notify-send", "-a", "KDE Connect", "-i", "smartphone", "Ping from "+dev.Name(), msg).Run()
	}()

	return nil
}

func (p *PingPlugin) OnConnect(dev device.Sender)    {}
func (p *PingPlugin) OnDisconnect(dev device.Sender) {}
