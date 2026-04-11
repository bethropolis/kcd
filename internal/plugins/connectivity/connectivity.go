package connectivity

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
)

type ConnectivityPlugin struct {
	bus *events.Bus
}

func NewConnectivityPlugin(bus *events.Bus) *ConnectivityPlugin {
	return &ConnectivityPlugin{bus: bus}
}

type SignalStrength struct {
	NetworkType         string `json:"networkType"`
	NetworkDetailedType string `json:"networkDetailedType,omitempty"`
	SignalStrength      int    `json:"signalStrength"`
}

type ConnectivityBody struct {
	SignalStrengths map[string]SignalStrength `json:"signalStrengths"`
}

func (p *ConnectivityPlugin) Name() string           { return "Connectivity" }
func (p *ConnectivityPlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *ConnectivityPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.connectivity_report"}
}
func (p *ConnectivityPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.connectivity_report.request"}
}

func (p *ConnectivityPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body ConnectivityBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	if p.bus != nil {
		p.bus.Publish(events.TypeConnectivityUpdate, dev.ID(), body)
	}
	return nil
}

func (p *ConnectivityPlugin) OnConnect(dev device.Sender) {
	// Request an immediate connectivity report upon connection
	pkt, _ := protocol.NewPacket("kdeconnect.connectivity_report.request", map[string]interface{}{})
	dev.Send(pkt)
}

func (p *ConnectivityPlugin) OnDisconnect(dev device.Sender) {}
