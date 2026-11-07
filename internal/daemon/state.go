package daemon

import (
	"context"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
)

type stateManager struct {
	devices        *device.Registry
	statePath      string
	pruneThreshold time.Duration
	logger         log.Logger
	saveQueue      chan struct{}
	done           chan struct{}
}

func initStateManager(ctx context.Context, cfg *config.Config, devices *device.Registry, logger log.Logger) (*stateManager, func(), func() error, error) {
	statePath := cfg.StatePath()
	stateLock, err := config.AcquireStateLock(statePath)
	if err != nil {
		return nil, nil, nil, err
	}
	defer stateLock.Close()

	if err := devices.Load(statePath, logger); err != nil {
		logger.Warn("failed to load devices", log.Error(err))
	}

	pruneThreshold, _ := time.ParseDuration(cfg.PruneStaleThreshold)
	if pruned := devices.Prune(pruneThreshold); pruned > 0 {
		logger.Info("pruned stale devices on startup", log.Int("count", pruned))
	}

	sm := &stateManager{
		devices:        devices,
		statePath:      statePath,
		pruneThreshold: pruneThreshold,
		logger:         logger,
		saveQueue:      make(chan struct{}, 1),
		done:           make(chan struct{}),
	}

	requestSave := func() {
		select {
		case sm.saveQueue <- struct{}{}:
		default:
		}
	}

	saveDevices := func() error {
		devices.Prune(pruneThreshold)
		return devices.Save(statePath)
	}

	go sm.run(ctx, saveDevices)

	return sm, requestSave, saveDevices, nil
}

func (sm *stateManager) run(ctx context.Context, saveDevices func() error) {
	defer close(sm.done)
	for {
		select {
		case <-sm.saveQueue:
			if err := saveDevices(); err != nil {
				sm.logger.Error("device state save failed", log.Error(err))
			}
		case <-ctx.Done():
			if err := saveDevices(); err != nil {
				sm.logger.Error("device state save failed", log.Error(err))
			}
			return
		}
	}
}

func (sm *stateManager) wait() {
	<-sm.done
}
