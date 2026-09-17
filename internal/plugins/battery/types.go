package battery

import (
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
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
	notifications config.NotificationConfig
	cfg           config.BatteryConfig
	bus           *events.Bus
	logger        *zap.Logger
}

// NewBatteryPlugin creates a BatteryPlugin.
func NewBatteryPlugin(cfg config.BatteryConfig, bus *events.Bus, logger *zap.Logger, notifications ...config.NotificationConfig) *BatteryPlugin {
	var notificationCfg config.NotificationConfig
	if len(notifications) > 0 {
		notificationCfg = notifications[0]
	}
	return &BatteryPlugin{
		notifications: notificationCfg,
		cfg:           cfg,
		bus:           bus,
		logger:        logger.With(zap.String("plugin", "battery")),
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
