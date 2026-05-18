package daemon

import (
	"crypto/tls"
	"crypto/x509"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/battery"
	"github.com/bethropolis/kcd/internal/plugins/clipboard"
	"github.com/bethropolis/kcd/internal/plugins/connectivity"
	"github.com/bethropolis/kcd/internal/plugins/findmyphone"
	"github.com/bethropolis/kcd/internal/plugins/lockdevice"
	"github.com/bethropolis/kcd/internal/plugins/mousepad"
	"github.com/bethropolis/kcd/internal/plugins/mpris"
	"github.com/bethropolis/kcd/internal/plugins/notification"
	"github.com/bethropolis/kcd/internal/plugins/pair"
	"github.com/bethropolis/kcd/internal/plugins/ping"
	"github.com/bethropolis/kcd/internal/plugins/presenter"
	"github.com/bethropolis/kcd/internal/plugins/runcommand"
	"github.com/bethropolis/kcd/internal/plugins/sendnotification"
	"github.com/bethropolis/kcd/internal/plugins/sftp"
	"github.com/bethropolis/kcd/internal/plugins/share"
	"github.com/bethropolis/kcd/internal/plugins/sms"
	"github.com/bethropolis/kcd/internal/plugins/systemvolume"
	"github.com/bethropolis/kcd/internal/plugins/telephony"
	"go.uber.org/zap"
)

func setupPlugins(cfg *config.Config, bus *events.Bus, tlsCfg *tls.Config, logger *zap.Logger, devices *device.Registry, localCert *x509.Certificate, saveDevices func(), plugins *plugin.Registry) *pair.PairPlugin {
	pairPlugin := pair.NewPairPlugin(devices, localCert, cfg.Pairing, saveDevices, bus, logger)
	plugins.Register(pairPlugin)
	if cfg.Plugins.Battery {
		plugins.Register(battery.NewBatteryPlugin(cfg.Battery, bus, logger))
	}
	if cfg.Plugins.Notification {
		plugins.Register(notification.NewNotificationPlugin(cfg.Notification, bus, tlsCfg, logger))
	}
	if cfg.Plugins.Clipboard {
		plugins.Register(clipboard.NewClipboardPlugin(tlsCfg, logger))
	}
	if cfg.Plugins.Share {
		plugins.Register(share.NewSharePlugin(cfg.DownloadDir, cfg.Share, tlsCfg, bus, logger))
	}
	if cfg.Plugins.RunCommand {
		plugins.Register(runcommand.NewRunCommandPlugin(cfg.Commands, logger))
	}
	if cfg.Plugins.Ping {
		plugins.Register(ping.NewPingPlugin(cfg.Ping, bus, logger))
	}
	if cfg.Plugins.Telephony {
		plugins.Register(telephony.NewTelephonyPluginWithOptions(bus, cfg.Plugins.PauseMusic, logger))
	}
	if cfg.Plugins.Connectivity {
		plugins.Register(connectivity.NewConnectivityPlugin(bus))
	}
	if cfg.Plugins.MPRIS {
		plugins.Register(mpris.NewMPRISPlugin(tlsCfg, logger))
	}
	if cfg.Plugins.Mousepad {
		plugins.Register(mousepad.NewMousepadPlugin(cfg.Mousepad, logger))
	}
	if cfg.Plugins.Presenter {
		plugins.Register(presenter.NewPresenterPlugin(logger))
	}
	if cfg.Plugins.SFTP {
		plugins.Register(sftp.NewSftpPlugin(cfg.SFTP, bus, logger))
	}
	if cfg.Plugins.FindMyPhone {
		plugins.Register(findmyphone.NewFindMyPhonePlugin())
	}
	if cfg.Plugins.LockDevice {
		plugins.Register(lockdevice.NewLockDevicePlugin(logger))
	}
	if cfg.Plugins.SystemVolume {
		plugins.Register(systemvolume.NewSystemVolumePlugin(bus, logger))
	}
	if cfg.Plugins.SendNotifications {
		plugins.Register(sendnotification.NewSendNotificationPlugin(logger, devices))
	}
	if cfg.Plugins.SMS {
		plugins.Register(sms.NewSMSPlugin())
	}

	return pairPlugin
}
