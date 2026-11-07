package daemon

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/notification"
	"github.com/bethropolis/kcd/internal/plugins/runcommand"
)

// watchConfigReload installs a SIGHUP handler to reload dynamic configuration.
func watchConfigReload(ctx context.Context, cfg *config.Config, devices *device.Registry, plugins *plugin.Registry, logger log.Logger) {
	sighupCh := make(chan os.Signal, 1)
	signal.Notify(sighupCh, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sighupCh:
				logger.Info("SIGHUP received, reloading config")
				newCfg, err := config.Load(cfg.ConfigPath)
				if err != nil {
					logger.Error("config reload failed", log.Error(err))
					continue
				}

				if rc, ok := plugin.Lookup[*runcommand.RunCommandPlugin](plugins); ok {
					rc.SetCommands(newCfg.Commands, newCfg.CommandsPerDevice)
					for _, dev := range devices.Connected() {
						if err := rc.SendCommandList(dev); err != nil {
							logger.Warn("failed to push commands on reload",
								log.String("device_id", dev.ID()),
								log.Error(err),
							)
						}
					}
					logger.Info("reloaded commands",
						log.Int("global", len(newCfg.Commands)),
						log.Int("per_device", len(newCfg.CommandsPerDevice)),
					)
				}

				if notifPl, ok := plugin.Lookup[*notification.NotificationPlugin](plugins); ok {
					notifPl.SetFilters(newCfg.Notifications.Filters())
					logger.Info("reloaded notification filters")
				}

				logger.SetLevel(newCfg.LogLevel)
			}
		}
	}()
}
