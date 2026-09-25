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

// reconnectTriggerMinGap is the minimum spacing between sighting-triggered
// dials. Announcements can arrive in bursts (IPv4/IPv6 alternation, AP
// flicker); the gap keeps a burst to a single dial while the fallback
// timer keeps spacing untriggered attempts.
const reconnectTriggerMinGap = 5 * time.Second

// reconnectWithBackoff dials a paired device after it disconnects, using
// exponential backoff between attempts. It stops as soon as:
//   - the device reconnects (IsConnected becomes true), or
//   - the daemon context is cancelled, or
//   - the device is unpaired.
//
// With sighting-driven mode (reconnect.sighting_driven, the default) the
// loop parks instead of spinning a timer per backoff step: a discovery
// sighting (the peer provably alive at an address) dials immediately, and
// the fallback timer escalates to fallback_max (default 1h) for the silent
// case. Past stale_after (default 24h) without any sighting the loop gives
// up entirely — a future sighting respawns it from the discovery path.
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

		// Stop if the peer has been silent past the horizon: no sighting
		// for a day means it is gone, not roaming. Zero timers until a
		// future sighting respawns this loop from the discovery path.
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

		// Reload the dial target every lap: a sighting while parked
		// records a fresher address (roam) than this loop's spawn-time
		// target, and the one-shot discovery dial may have failed. The
		// spawn address stays the fallback when nothing was ever sighted.
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

		// Prefer the peer's last advertised listening port over the
		// default: the identity may carry a non-standard port (or none
		// at all, in which case LastPort is 0 and we fall back).
		port := reconnectPort(dev, opts.TCPPort)
		DialDevice(ctx, dialIP, port, dev.ID(), protocol.ProtocolVersion, identity, cfg, devices, plugins, localDeviceID, logger, false, opts)
		lastDial = time.Now()

		if dev.IsConnected() {
			logger.Info("auto-reconnect: succeeded",
				log.String("device_id", dev.ID()),
				log.Int("attempts", attempt+1),
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
