package sendnotification

import (
	"context"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// SendNotificationPlugin forwards desktop notifications to the phone.
// TODO: Re-implement notification monitoring without godbus (e.g. via gdbus monitor or dbus-monitor).
// The previous implementation was removed to eliminate the godbus dependency as part of the MPRIS migration.
type SendNotificationPlugin struct {
	logger  *zap.Logger
	devices *device.Registry
}

func NewSendNotificationPlugin(logger *zap.Logger, devices *device.Registry) *SendNotificationPlugin {
	return &SendNotificationPlugin{
		logger:  logger.With(zap.String("plugin", "sendnotification")),
		devices: devices,
	}
}

func (p *SendNotificationPlugin) Name() string            { return "SendNotification" }
func (p *SendNotificationPlugin) Timeout() time.Duration  { return 5 * time.Second }
func (p *SendNotificationPlugin) IncomingTypes() []string { return []string{} }
func (p *SendNotificationPlugin) OutgoingTypes() []string { return []string{"kdeconnect.notification"} }

func (p *SendNotificationPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	return nil
}

func (p *SendNotificationPlugin) OnConnect(dev device.Sender)    {}
func (p *SendNotificationPlugin) OnDisconnect(dev device.Sender) {}
