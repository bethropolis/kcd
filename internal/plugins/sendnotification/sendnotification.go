// Package sendnotification implements the KDE Connect Send Notifications plugin.
// It monitors the local D-Bus Notifications interface and forwards desktop
// notifications to all paired, connected phone devices.
package sendnotification

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/godbus/dbus/v5"
	"go.uber.org/zap"
)

const (
	notifInterface = "org.freedesktop.Notifications"
	notifPath      = "/org/freedesktop/Notifications"
	notifMember    = "Notify"
)

// SendNotificationPlugin forwards desktop notifications to the phone.
type SendNotificationPlugin struct {
	logger  *zap.Logger
	devices *device.Registry

	mu       sync.Mutex
	conn     *dbus.Conn
	cancelFn context.CancelFunc
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

// Handle is a no-op — this plugin only sends packets.
func (p *SendNotificationPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	return nil
}

// OnConnect starts monitoring D-Bus notifications when a device connects.
func (p *SendNotificationPlugin) OnConnect(dev device.Sender) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn != nil {
		return // already monitoring
	}

	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		p.logger.Warn("sendnotification: DBUS_SESSION_BUS_ADDRESS not set, skipping D-Bus monitor")
		return
	}

	conn, err := dbus.SessionBus()
	if err != nil {
		p.logger.Warn("sendnotification: failed to connect to session D-Bus", zap.Error(err))
		return
	}

	// Become a monitor for org.freedesktop.Notifications.Notify calls
	rule := fmt.Sprintf(
		"type='method_call',interface='%s',member='%s'",
		notifInterface, notifMember,
	)
	if err := conn.BusObject().Call("org.freedesktop.DBus.Monitoring.BecomeMonitor",
		0, []string{rule}, uint32(0)).Err; err != nil {
		// Fallback: use AddMatch for non-monitor mode (works on most systems)
		if err2 := conn.AddMatchSignal(
			dbus.WithMatchInterface(notifInterface),
		); err2 != nil {
			p.logger.Warn("sendnotification: could not monitor Notifications D-Bus", zap.Error(err))
			conn.Close()
			return
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.conn = conn
	p.cancelFn = cancel

	go p.monitorLoop(ctx, conn)
	p.logger.Info("sendnotification: started monitoring desktop notifications")
}

// OnDisconnect stops monitoring when the last device disconnects.
func (p *SendNotificationPlugin) OnDisconnect(dev device.Sender) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Only stop if no other devices are connected.
	if p.devices != nil {
		connected := 0
		for _, d := range p.devices.List() {
			if d.IsConnected() {
				connected++
			}
		}
		if connected > 0 {
			return
		}
	}

	if p.cancelFn != nil {
		p.cancelFn()
		p.cancelFn = nil
	}
	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
	}
}

func (p *SendNotificationPlugin) monitorLoop(ctx context.Context, conn *dbus.Conn) {
	ch := make(chan *dbus.Message, 64)
	conn.Eavesdrop(ch)

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			p.handleDBusMessage(msg)
		}
	}
}

func (p *SendNotificationPlugin) handleDBusMessage(msg *dbus.Message) {
	if msg == nil || len(msg.Body) < 8 {
		return
	}

	// Notify(app_name, replaces_id, app_icon, summary, body, actions, hints, expire_timeout)
	appName, _ := msg.Body[0].(string)
	summary, _ := msg.Body[3].(string)
	body, _ := msg.Body[4].(string)

	if appName == "kcd" || summary == "" {
		return // skip our own notifications
	}

	notifPkt, err := protocol.NewPacket("kdeconnect.notification", map[string]interface{}{
		"id":          fmt.Sprintf("desktop-%d", time.Now().UnixNano()),
		"appName":     appName,
		"ticker":      summary,
		"title":       summary,
		"text":        body,
		"isClearable": true,
	})
	if err != nil {
		return
	}

	if p.devices == nil {
		return
	}
	// Fan out to all paired and connected devices.
	for _, dev := range p.devices.List() {
		if dev.IsConnected() {
			if err := dev.Send(notifPkt); err != nil {
				p.logger.Debug("sendnotification: send failed", zap.String("device", dev.ID()), zap.Error(err))
			}
		}
	}
}
