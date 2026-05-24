package connectivity

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
)

type ConnectivityPlugin struct {
	bus         *events.Bus
	mu          sync.Mutex
	lastReports map[string]ConnectivityBody
}

func NewConnectivityPlugin(bus *events.Bus) *ConnectivityPlugin {
	return &ConnectivityPlugin{
		bus:         bus,
		lastReports: make(map[string]ConnectivityBody),
	}
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

	if p.shouldPublish(dev.ID(), body) && p.bus != nil {
		p.bus.Publish(events.TypeConnectivityUpdate, dev.ID(), body)
	}
	return nil
}

func (p *ConnectivityPlugin) shouldPublish(deviceID string, body ConnectivityBody) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	last, ok := p.lastReports[deviceID]
	if ok && connectivityEqual(last, body) {
		return false
	}
	if p.lastReports == nil {
		p.lastReports = make(map[string]ConnectivityBody)
	}
	p.lastReports[deviceID] = body
	return true
}

func connectivityEqual(a, b ConnectivityBody) bool {
	if len(a.SignalStrengths) != len(b.SignalStrengths) {
		return false
	}
	for key, av := range a.SignalStrengths {
		bv, ok := b.SignalStrengths[key]
		if !ok || av != bv {
			return false
		}
	}
	return true
}

func (p *ConnectivityPlugin) OnConnect(dev device.Sender) {
	// Request an immediate connectivity report upon connection
	pkt, _ := protocol.NewPacket("kdeconnect.connectivity_report.request", map[string]interface{}{})
	dev.Send(pkt)
}

func (p *ConnectivityPlugin) OnDisconnect(dev device.Sender) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.lastReports, dev.ID())
}
