package battery

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
)

// BatteryPlugin handles incoming battery state updates.
type BatteryPlugin struct{}

// BatteryBody represents the body of a kdeconnect.battery packet.
type BatteryBody struct {
	CurrentCharge  int  `json:"currentCharge"`
	IsCharging     bool `json:"isCharging"`
	ThresholdEvent int  `json:"thresholdEvent"`
}

// Name returns the plugin name.
func (p *BatteryPlugin) Name() string { return "Battery" }

// Timeout returns the timeout.
func (p *BatteryPlugin) Timeout() time.Duration { return 5 * time.Second }

// IncomingTypes returns the packet types this plugin handles.
func (p *BatteryPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.battery"}
}

// OutgoingTypes returns the packet types this plugin may send.
func (p *BatteryPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.battery.request"}
}

// Handle processes incoming battery updates.
func (p *BatteryPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body BatteryBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	dev.UpdateBattery(body.CurrentCharge, body.IsCharging)
	return nil
}

func (p *BatteryPlugin) OnConnect(dev device.Sender) {
	pkt, _ := protocol.NewPacket("kdeconnect.battery.request", map[string]interface{}{
		"request": true,
	})
	dev.Send(pkt)
}

func (p *BatteryPlugin) OnDisconnect(dev device.Sender) {
}
