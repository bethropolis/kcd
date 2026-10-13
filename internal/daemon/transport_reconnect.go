package daemon

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
)

// reconnectTriggerMinGap spaces sighting-triggered dials: announcements
// arrive in bursts, and a burst must dial only once.
const reconnectTriggerMinGap = 5 * time.Second

// reconnectWithBackoff redials a disconnected pair, escalating to fallback_max
// and stopping past stale_after (a later sighting respawns it) or on reconnect.
func reconnectWithBackoff(
	ctx context.Context,
	dev *device.Device,
	ip net.IP,
	identity *protocol.Packet,
	cfg *tls.Config,
	devices *device.Registry,
	plugins *plugin.Registry,
	localDeviceID string,
	logger log.Logger,
	opts *config.Config,
) {
	maxBackoff := config.Duration(opts.Reconnect.MaxBackoff)
	waitCap := maxBackoff
	sightingDriven := opts.Reconnect.SightingDriven
	staleAfter := time.Duration(0)
	if sightingDriven {
		waitCap = config.Duration(opts.Reconnect.FallbackMax)
		staleAfter = config.Duration(opts.Reconnect.StaleAfter)
	}
	attempt := dev.ReconnectAttempt()
	// Zero until the first dial: the trigger gap guards between dials,
	// and loop start is not a dial — the first sighting always dials.
	var lastDial time.Time

	defer dev.ReconnectDone()

	logger.Info("starting auto-reconnect",
		log.String("device_id", dev.ID()),
		log.String("device_name", dev.Name()),
		log.String("ip", ip.String()),
		log.Bool("sighting_driven", sightingDriven),
	)

	for {
		// Stop if the daemon is shutting down.
		if ctx.Err() != nil {
			return
		}

		// Stop if the device was unpaired while we were waiting.
		if dev.State() != device.StatePaired {
			logger.Debug("auto-reconnect: device no longer paired, stopping",
				log.String("device_id", dev.ID()))
			return
		}

		// Stop if the device already reconnected (inbound connection from phone).
		if dev.IsConnected() {
			logger.Debug("auto-reconnect: device already connected, stopping",
				log.String("device_id", dev.ID()))
			return
		}

		// Stop past the silent horizon: a day without sightings means gone, not
		// roaming. Zero timers until a future sighting respawns this loop.
		if sightingDriven && time.Since(dev.LastSeen()) > staleAfter {
			logger.Info("auto-reconnect: device stale, giving up until next sighting",
				log.String("device_id", dev.ID()),
				log.Duration("stale_after", staleAfter),
			)
			return
		}

		backoff := device.ReconnectBackoff(attempt, waitCap, config.Duration(opts.Reconnect.InitialBackoff))
		logger.Debug("auto-reconnect: waiting before next attempt",
			log.String("device_id", dev.ID()),
			log.Int("attempt", attempt+1),
			log.Duration("backoff", backoff),
		)

		timer := time.NewTimer(backoff)
		triggered := false
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-dev.ReconnectWake():
			// Sighting (peer alive) or unpair. Stop the timer and
			// re-check below; a fresh sighting dials immediately.
			timer.Stop()
			triggered = true
		case <-timer.C:
		}

		// Re-check after the wait — the phone may have connected inbound,
		// or been unpaired while parked.
		if dev.IsConnected() || dev.State() != device.StatePaired {
			return
		}

		if triggered && time.Since(lastDial) < reconnectTriggerMinGap {
			// Sighting burst (dual-stack/AP flicker): skip this dial,
			// keep parking. The backoff is recomputed next lap.
			continue
		}

		// Reload the dial target every lap: parked sightings record fresher
		// (roamed) addresses; the spawn address stays the never-sighted fallback.
		dialIP := ip
		if sighted := dev.LastSightedIP(); sighted != nil {
			dialIP = sighted
		}

		logger.Info("auto-reconnect: dialling",
			log.String("device_id", dev.ID()),
			log.String("ip", dialIP.String()),
			log.Int("attempt", attempt+1),
			log.Bool("sighting_triggered", triggered),
		)

		// Prefer the peer's advertised listening port over the default; zero
		// (absent) falls back.
		port := reconnectPort(dev, opts.TCPPort)
		DialDevice(ctx, dialIP, port, dev.ID(), protocol.ProtocolVersion, identity, cfg, devices, plugins, localDeviceID, logger, false, opts)
		lastDial = time.Now()

		if dev.IsConnected() {
			logger.Info("auto-reconnect: succeeded",
				log.String("device_id", dev.ID()),
				log.Int("attempts", attempt+1),
			)
			// Persist the counter: a flap continues backing off next cycle;
			// onDisconnect resets it for connections that proved stable.
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
