package device

import (
	"crypto/x509"
	"net"
	"time"

	"github.com/bethropolis/kcd/internal/events"
)

// UpdateBattery updates the battery state of the device.
func (d *Device) UpdateBattery(charge int, charging bool) {
	d.mu.Lock()
	d.BatteryCharge = charge
	d.IsCharging = charging
	d.batterySeen = true
	d.lastBatteryAt = time.Now()
	bus := d.bus
	id := d.id
	d.mu.Unlock()

	if bus != nil {
		bus.Publish(events.TypeBatteryUpdate, id, map[string]interface{}{
			"charge":   charge,
			"charging": charging,
		})
	}
}

// GetBattery returns the current battery state of the device.
func (d *Device) GetBattery() (int, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.BatteryCharge, d.IsCharging
}

// HasBattery reports whether at least one battery packet was received.
// Until then the charge values are zero-value defaults, not measurements.
func (d *Device) HasBattery() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.batterySeen
}

// BatteryAge returns how long ago the last battery packet arrived, or a
// negative duration when no packet was ever received.
func (d *Device) BatteryAge() time.Duration {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if !d.batterySeen {
		return -1
	}
	return time.Since(d.lastBatteryAt)
}

// HasCapability checks if the device has a particular capability (incoming or outgoing).
func (d *Device) HasCapability(cap string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for _, c := range d.IncomingCaps {
		if c == cap {
			return true
		}
	}
	for _, c := range d.OutgoingCaps {
		if c == cap {
			return true
		}
	}
	return false
}

// RemoteIP returns the IP address of the connected peer, if available.
func (d *Device) RemoteIP() net.IP {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.conn == nil {
		return nil
	}
	addr := d.conn.RemoteAddr()
	if tcpAddr, ok := addr.(*net.TCPAddr); ok {
		return tcpAddr.IP
	}
	return nil
}

// LastIP returns the IP address from the most recent successful connection.
// Unlike RemoteIP, this persists after the connection drops — safe to use
// from an OnDisconnect callback for auto-reconnect dialling.
func (d *Device) LastIP() net.IP {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.lastIP
}

// PeerCert returns the validated certificate presented by the remote device.
// Returns nil if not connected or no certificate was presented.
func (d *Device) PeerCert() *x509.Certificate {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.conn == nil {
		return nil
	}
	return d.conn.PeerCert()
}
