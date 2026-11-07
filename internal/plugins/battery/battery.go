package battery

import (
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/sysexec"
)

var _ plugin.Plugin = (*BatteryPlugin)(nil)

// NewBatteryPlugin creates a BatteryPlugin.
func NewBatteryPlugin(cfg config.BatteryConfig, bus *events.Bus, logger log.Logger, notifications ...config.NotificationConfig) *BatteryPlugin {
	var notificationCfg config.NotificationConfig
	if len(notifications) > 0 {
		notificationCfg = notifications[0]
	}
	return &BatteryPlugin{
		notifications: notificationCfg,
		cfg:           cfg,
		bus:           bus,
		logger:        logger.With(log.String("plugin", "battery")),
		exec:          sysexec.New(nil),
	}
}

func (p *BatteryPlugin) Name() string           { return "Battery" }
func (p *BatteryPlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *BatteryPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.battery", "kdeconnect.battery.request"}
}
func (p *BatteryPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.battery"}
}

// OnConnect sends our local battery state if available.
func (p *BatteryPlugin) OnConnect(dev device.Peer) {
	charge, charging, err := readLocalBattery()
	if err != nil {
		p.logger.Debug("local battery unavailable on connect", log.Error(err))
		return
	}
	pkt, err := protocol.NewPacket("kdeconnect.battery", BatteryBody{
		CurrentCharge: charge,
		IsCharging:    charging,
	})
	if err == nil {
		_ = dev.Send(pkt)
	}
}

func (p *BatteryPlugin) OnDisconnect(_ device.Peer) {}
