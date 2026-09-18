package daemon

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// reconnectWithBackoff dials a paired device after it disconnects, using
// exponential backoff up to 5 minutes between attempts. It stops as soon as:
//   - the device reconnects (IsConnected becomes true), or
//   - the daemon context is cancelled, or
//   - the device is unpaired.
//
// A fresh connection coming in from the phone side (inbound TCP) will set
// IsConnected, causing the loop to exit cleanly without a duplicate dial.
func reconnectWithBackoff(
	ctx context.Context,
	dev *device.Device,
	ip net.IP,
	identity *protocol.Packet,
	cfg *tls.Config,
	devices *device.Registry,
	plugins *plugin.Registry,
	localDeviceID string,
	logger *zap.Logger,
	opts *config.Config,
) {
	maxBackoff := config.Duration(opts.Reconnect.MaxBackoff)
	attempt := dev.ReconnectAttempt()

	defer dev.ReconnectDone()

	logger.Info("starting auto-reconnect",
		zap.String("device_id", dev.ID()),
		zap.String("device_name", dev.Name()),
		zap.String("ip", ip.String()),
	)

	for {
		// Stop if the daemon is shutting down.
		if ctx.Err() != nil {
			return
		}

		// Stop if the device was unpaired while we were waiting.
		if dev.State() != device.StatePaired {
			logger.Debug("auto-reconnect: device no longer paired, stopping",
				zap.String("device_id", dev.ID()))
			return
		}

		// Stop if the device already reconnected (inbound connection from phone).
		if dev.IsConnected() {
			logger.Debug("auto-reconnect: device already connected, stopping",
				zap.String("device_id", dev.ID()))
			return
		}

		backoff := device.ReconnectBackoff(attempt, maxBackoff, config.Duration(opts.Reconnect.InitialBackoff))
		logger.Debug("auto-reconnect: waiting before next attempt",
			zap.String("device_id", dev.ID()),
			zap.Int("attempt", attempt+1),
			zap.Duration("backoff", backoff),
		)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		// Re-check after the sleep — the phone may have connected inbound.
		if dev.IsConnected() || dev.State() != device.StatePaired {
			return
		}

		logger.Info("auto-reconnect: dialling",
			zap.String("device_id", dev.ID()),
			zap.String("ip", ip.String()),
			zap.Int("attempt", attempt+1),
		)

		// Prefer the peer's last advertised listening port over the
		// default: the identity may carry a non-standard port (or none
		// at all, in which case LastPort is 0 and we fall back).
		port := reconnectPort(dev, opts.TCPPort)
		DialDevice(ctx, ip, port, dev.ID(), protocol.ProtocolVersion, identity, cfg, devices, plugins, localDeviceID, logger, false, opts)

		if dev.IsConnected() {
			logger.Info("auto-reconnect: succeeded",
				zap.String("device_id", dev.ID()),
				zap.Int("attempts", attempt+1),
			)
			// Persist the counter before returning: if this connection flaps,
			// the next reconnect cycle continues backing off rather than
			// resetting to the 2s floor. onDisconnect resets it if the
			// connection proved stable.
			dev.SetReconnectAttempt(attempt + 1)
			return
		}

		attempt++
	}
}

// reconnectPort prefers the authenticated peer port, falling back to local configuration.
func reconnectPort(dev *device.Device, fallback int) int {
	if p := dev.LastPort(); validDialPort(p) {
		return p
	}
	return fallback
}
