package battery

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

// ThresholdEvent values from the KDE Connect protocol.
const (
	thresholdNone    = 0
	thresholdLow     = 1 // battery is low (typically <= 15%)
	thresholdFull    = 2 // battery reached full charge
)

// BatteryPlugin handles incoming battery state updates.
type BatteryPlugin struct {
	cfg    config.BatteryConfig
	bus    *events.Bus
	logger *zap.Logger
}

// NewBatteryPlugin creates a BatteryPlugin.
func NewBatteryPlugin(cfg config.BatteryConfig, bus *events.Bus, logger *zap.Logger) *BatteryPlugin {
	return &BatteryPlugin{
		cfg:    cfg,
		bus:    bus,
		logger: logger.With(zap.String("plugin", "battery")),
	}
}

// BatteryBody represents the body of a kdeconnect.battery packet.
type BatteryBody struct {
	CurrentCharge  int  `json:"currentCharge"`
	IsCharging     bool `json:"isCharging"`
	ThresholdEvent int  `json:"thresholdEvent"`
}

func (p *BatteryPlugin) Name() string            { return "Battery" }
func (p *BatteryPlugin) Timeout() time.Duration  { return 5 * time.Second }
func (p *BatteryPlugin) IncomingTypes() []string { return []string{"kdeconnect.battery"} }
func (p *BatteryPlugin) OutgoingTypes() []string { return []string{"kdeconnect.battery.request"} }

// Handle processes incoming battery updates.
func (p *BatteryPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body BatteryBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	// Update the device's cached battery state.
	// UpdateBattery also publishes TypeBatteryUpdate via the device's own bus reference.
	dev.UpdateBattery(body.CurrentCharge, body.IsCharging)

	// Handle threshold events — these warrant a desktop notification in addition
	// to the standard battery.update event.
	if body.ThresholdEvent != thresholdNone {
		p.handleThreshold(dev, body)
	}

	return nil
}

func (p *BatteryPlugin) handleThreshold(dev device.Sender, body BatteryBody) {
	var message, urgency string

	switch body.ThresholdEvent {
	case thresholdLow:
		if !p.cfg.NotifyLow {
			break
		}
		message = p.cfg.LowMessage
		urgency = p.cfg.LowUrgency
	case thresholdFull:
		if !p.cfg.NotifyFull {
			break
		}
		message = p.cfg.FullMessage
		urgency = p.cfg.FullUrgency
	default:
		return
	}

	if message != "" {
		// Desktop notification.
		plugin.RunCommandAsync(p.logger, "notify-send",
			"-a", "KDE Connect",
			"-u", urgency,
			"-i", "battery",
			dev.Name(), message,
		)
	}

	// Emit event so watch / scripts can react.
	if p.bus != nil {
		p.bus.Publish(events.TypeBatteryThreshold, dev.ID(), map[string]any{
			"charge":   body.CurrentCharge,
			"charging": body.IsCharging,
			"event":    body.ThresholdEvent,
		})
	}
}

// OnConnect requests the current battery state immediately on connection.
func (p *BatteryPlugin) OnConnect(dev device.Sender) {
	pkt, _ := protocol.NewPacket("kdeconnect.battery.request", map[string]any{
		"request": true,
	})
	dev.Send(pkt)
}

func (p *BatteryPlugin) OnDisconnect(_ device.Sender) {}
