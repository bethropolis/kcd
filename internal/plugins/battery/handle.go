package battery

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// Handle processes incoming battery packets.
func (p *BatteryPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	switch pkt.Type {
	case "kdeconnect.battery":
		var body BatteryBody
		if err := json.Unmarshal(pkt.Body, &body); err != nil {
			return fmt.Errorf("battery: decode body: %w", err)
		}

		dev.UpdateBattery(body.CurrentCharge, body.IsCharging)

		if body.ThresholdEvent != thresholdNone {
			p.handleThreshold(dev, body)
		}

	case "kdeconnect.battery.request":
		// The phone is asking for our local battery state.
		charge, charging, err := readLocalBattery()
		if err != nil {
			p.logger.Debug("local battery unavailable, skipping response", zap.Error(err))
			return nil
		}
		pkt, err := protocol.NewPacket("kdeconnect.battery", BatteryBody{
			CurrentCharge: charge,
			IsCharging:    charging,
		})
		if err != nil {
			return fmt.Errorf("battery: create response packet: %w", err)
		}
		return dev.Send(pkt)
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
			"-a", p.notifications.AppName(),
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

// OnConnect requests the phone's battery and sends our local battery state.
func (p *BatteryPlugin) OnConnect(dev device.Sender) {
	// Ask phone for its battery.
	pkt, _ := protocol.NewPacket("kdeconnect.battery.request", map[string]any{
		"request": true,
	})
	dev.Send(pkt)

	// Send our local battery to the phone.
	charge, charging, err := readLocalBattery()
	if err != nil {
		p.logger.Debug("local battery unavailable on connect", zap.Error(err))
		return
	}
	pkt, _ = protocol.NewPacket("kdeconnect.battery", BatteryBody{
		CurrentCharge: charge,
		IsCharging:    charging,
	})
	dev.Send(pkt)
}

func (p *BatteryPlugin) OnDisconnect(_ device.Sender) {}
