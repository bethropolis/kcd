package device

import (
	"net"
	"time"
)

// SetDiscoveryAddr records where the device was last seen announcing itself.
func (d *Device) SetDiscoveryAddr(ip net.IP, port int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.discoveryIP = ip
	d.discoveryPort = port
}

// DiscoveryAddr returns the last-seen announcement address, or nil if unknown.
func (d *Device) DiscoveryAddr() (net.IP, int) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.discoveryIP, d.discoveryPort
}

// pairDialIntentTTL bounds how long an explicit `kcd pair <id>` intent pins
// a connection while pairing hasn't started yet.
const pairDialIntentTTL = 5 * time.Minute

// RequestPairDial marks the device for a one-shot outbound dial on its next
// discovery announcement and arms the keep-alive intent until pairing starts,
// is rejected, succeeds, or the TTL expires. Used when the user explicitly
// runs `kcd pair <id>` for a device with no active connection.
func (d *Device) RequestPairDial(intentTTL ...time.Duration) {
	ttl := pairDialIntentTTL
	if len(intentTTL) > 0 && intentTTL[0] > 0 {
		ttl = intentTTL[0]
	}
	d.pairDialRequested.Store(true)
	d.pairIntentUntil.Store(time.Now().Add(ttl).UnixNano())
}

// ConsumePairDial reports and clears a pending explicit pair-dial request.
func (d *Device) ConsumePairDial() bool {
	return d.pairDialRequested.CompareAndSwap(true, false)
}

// PairDialPending reports whether an explicit pair-dial was requested,
// without clearing it.
func (d *Device) PairDialPending() bool {
	return d.pairDialRequested.Load()
}

// PairDialActive reports whether an explicit pair intent is still keeping
// the connection alive: requested and neither cleared nor expired. Unlike
// PairDialPending (the one-shot dial trigger, consumed on first sighting),
// this survives dial/connect cycles until pairing starts or ends.
func (d *Device) PairDialActive() bool {
	return d.pairIntentUntil.Load() > time.Now().UnixNano()
}

// ClearPairDial drops both the one-shot trigger and the keep-alive intent.
// Call it when pairing completes, is rejected/cancelled, or is unpaired.
func (d *Device) ClearPairDial() {
	d.pairDialRequested.Store(false)
	d.pairIntentUntil.Store(0)
}

// LastPort returns the last authenticated tcpPort advertised by the peer,
// or 0 if unknown.
func (d *Device) LastPort() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.lastPort
}

// SetLastPort records the peer's advertised listening port after a
// successful authenticated exchange. Callers must pass a validated port.
func (d *Device) SetLastPort(port int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastPort = port
}

// SetLastIP records a dial target, used when restoring persisted state.
// A nil IP clears the target.
func (d *Device) SetLastIP(ip net.IP) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastIP = ip
}

// ShouldDiscoveryDial reports whether enough time has passed since the last
// discovery-triggered dial for this device, and marks this dial if so. It
// bounds redial storms to a stale LastIP (DHCP roam) or spoofed sightings.
func (d *Device) ShouldDiscoveryDial(minInterval time.Duration) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if time.Since(d.lastDiscoveryDial) < minInterval {
		return false
	}
	d.lastDiscoveryDial = time.Now()
	return true
}

// MarkEphemeralDialed records that the one ephemeral discovery dial for the
// current unpaired era has been used.
func (d *Device) MarkEphemeralDialed() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ephemeralDialed = true
}

// EphemeralDialed reports whether the ephemeral discovery dial was used.
func (d *Device) EphemeralDialed() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.ephemeralDialed
}

// ClearEphemeral makes the device eligible for a fresh ephemeral discovery
// dial (e.g. after an explicit unpair or rejection).
func (d *Device) ClearEphemeral() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ephemeralDialed = false
}

// InCooldown reports whether a handshake completed too recently to start
// another one for this device.
func (d *Device) InCooldown() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return !d.lastConnect.IsZero() && time.Since(d.lastConnect) < reconnectCooldown
}

// NoteSighting records a discovery sighting while disconnected, reporting
// whether it confirms a genuine roam: same new address twice running.
// Single sightings prove nothing (IPv4/IPv6 alternation, AP flicker).
func (d *Device) NoteSighting(sighted net.IP) (roamed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	defer func() { d.lastSightedIP = sighted }()
	if lastIP := d.lastIP; lastIP == nil || !lastIP.Equal(sighted) {
		return sighted.Equal(d.lastSightedIP)
	}
	return false
}

// LastSightedIP returns the most recent discovery sighting address, or
// nil if the peer has never announced itself in this process. Unlike
// LastIP (the last *authenticated* address), this tracks where the peer
// provably is right now — the parked reconnect loop prefers it over its
// spawn-time target so fallback attempts follow roams instead of
// redialling a stale address after a failed one-shot dial.
func (d *Device) LastSightedIP() net.IP {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.lastSightedIP
}

// TryReconnect attempts to mark the device as reconnecting.
// Returns true if this goroutine should proceed; false if another
// reconnect goroutine is already running.
func (d *Device) TryReconnect() bool {
	return d.reconnecting.CompareAndSwap(false, true)
}

// ReconnectDone marks the device as no longer reconnecting.
// Must be called (typically via defer) after a reconnect goroutine exits.
func (d *Device) ReconnectDone() {
	d.reconnecting.Store(false)
}

// ReconnectAttempt returns the persisted auto-reconnect backoff counter.
func (d *Device) ReconnectAttempt() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.reconnectAttempt
}

// SetReconnectAttempt stores the auto-reconnect backoff counter so the next
// reconnect cycle (spawned when this connection drops) continues backing off
// instead of resetting to the initial floor.
func (d *Device) SetReconnectAttempt(n int) {
	d.mu.Lock()
	d.reconnectAttempt = n
	d.mu.Unlock()
}

// ResetReconnectAttempt clears the backoff counter after a stable connection
// (one that stayed up long enough to count as genuinely healthy) drops.
func (d *Device) ResetReconnectAttempt() {
	d.mu.Lock()
	d.reconnectAttempt = 0
	d.mu.Unlock()
}

// ConnectionAge returns how long the current connection has been established,
// or 0 if the device has never connected in this process.
func (d *Device) ConnectionAge() time.Duration {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.connectStarted.IsZero() {
		return 0
	}
	return time.Since(d.connectStarted)
}
