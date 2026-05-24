package battery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	thresholdNone = 0
	thresholdLow  = 1 // battery is low (typically <= 15%)
	thresholdFull = 2 // battery reached full charge
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

func (p *BatteryPlugin) Name() string           { return "Battery" }
func (p *BatteryPlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *BatteryPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.battery", "kdeconnect.battery.request"}
}
func (p *BatteryPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.battery", "kdeconnect.battery.request"}
}

// Handle processes incoming battery packets.
func (p *BatteryPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	switch pkt.Type {
	case "kdeconnect.battery":
		var body BatteryBody
		if err := json.Unmarshal(pkt.Body, &body); err != nil {
			return err
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
			return err
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

// powerSupplyRoots lists paths to check for battery sysfs entries.
var powerSupplyRoots = []string{
	"/sys/class/power_supply",
	"/sys/devices/platform/subsystem/power_supply",
}

// readLocalBattery reads the local battery state from sysfs.
// Returns charge (0-100), charging status, and any error.
// If no battery is found, returns an error — callers should log and skip.
func readLocalBattery() (int, bool, error) {
	for _, root := range powerSupplyRoots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, "BAT") {
				continue
			}
			base := filepath.Join(root, name)

			// Read capacity (0-100)
			capRaw, err := os.ReadFile(filepath.Join(base, "capacity"))
			if err != nil {
				continue
			}
			capacity, err := strconv.Atoi(strings.TrimSpace(string(capRaw)))
			if err != nil {
				continue
			}

			// Read status (Charging/Discharging/Full/Unknown)
			statusRaw, _ := os.ReadFile(filepath.Join(base, "status"))
			status := strings.TrimSpace(string(statusRaw))
			charging := status == "Charging"

			return capacity, charging, nil
		}
	}

	return 0, false, errNoBattery
}

// errNoBattery is returned when no battery sysfs entry is found.
var errNoBattery = &noBatteryError{}

type noBatteryError struct{}

func (e *noBatteryError) Error() string { return "no battery found" }
func (e *noBatteryError) Is(target error) bool {
	_, ok := target.(*noBatteryError)
	return ok
}
