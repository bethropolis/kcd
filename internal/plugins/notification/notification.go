package notification

import (
	"context"
	"crypto/tls"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/sysexec"
	"github.com/bethropolis/kcd/internal/transport"
)

var _ plugin.Plugin = (*NotificationPlugin)(nil)

// NewNotificationPlugin creates a NotificationPlugin; nil tlsConfig disables
// icon fetching over the side-channel.
func NewNotificationPlugin(cfg config.NotificationPluginConfig, bus *events.Bus, tlsConfig *tls.Config, logger log.Logger, options ...transport.SidechannelOptions) *NotificationPlugin {
	popups := config.PopupsConfig{}
	popups.Defaults()
	return newNotificationPlugin(cfg, bus, tlsConfig, logger, popups, "auto", options...)
}

// NewNotificationPluginWithPopup creates a notification plugin with popup policy.
func NewNotificationPluginWithPopup(cfg config.NotificationPluginConfig, bus *events.Bus, tlsConfig *tls.Config, logger log.Logger, popups config.PopupsConfig, backend string, options ...transport.SidechannelOptions) *NotificationPlugin {
	return newNotificationPlugin(cfg, bus, tlsConfig, logger, popups, backend, options...)
}

func newNotificationPlugin(cfg config.NotificationPluginConfig, bus *events.Bus, tlsConfig *tls.Config, logger log.Logger, popups config.PopupsConfig, backend string, options ...transport.SidechannelOptions) *NotificationPlugin {
	var sidechannel transport.SidechannelOptions
	if len(options) > 0 {
		sidechannel = options[0]
	}
	p := &NotificationPlugin{
		sidechannel: sidechannel,
		cfg:         cfg,
		bus:         bus,
		tlsConfig:   tlsConfig,
		logger:      logger.With(log.String("plugin", "notification")),
		newExec:     exec.CommandContext,
		popups:      popups,
		backend:     backend,
	}

	// Probe --print-id support by checking --help output.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer probeCancel()
	if out, err := sysexec.Run(probeCtx, "notify-send", "--help"); err == nil {
		p.canCloseNotifs = strings.Contains(string(out), "--print-id")
	}

	baseDir := cfg.IconCacheDir
	if dir, err := os.MkdirTemp(baseDir, "kcd-notif-icons-*"); err == nil {
		p.iconDir = dir
	}

	return p
}

func (p *NotificationPlugin) Name() string           { return "Notification" }
func (p *NotificationPlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *NotificationPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.notification"}
}
func (p *NotificationPlugin) OutgoingTypes() []string {
	return []string{
		"kdeconnect.notification.reply",
		"kdeconnect.notification.request",
		"kdeconnect.notification.action",
	}
}

func (p *NotificationPlugin) OnConnect(_ device.Peer)    {}
func (p *NotificationPlugin) OnDisconnect(_ device.Peer) {}
