package device

import (
	"context"
	"crypto/x509"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
	"go.uber.org/zap"
)

// Device represents an active KDE Connect remote device.
type Device struct {
	id   string
	name string
	Type string

	IncomingCaps []string
	OutgoingCaps []string

	state  PairingState
	CertFP string

	lastSeen time.Time
	lastIP   net.IP // cached from last successful connection; survives Disconnect
	// lastPort is the tcpPort the peer last advertised over the authenticated
	// (post-TLS) identity exchange. Used with lastIP as the dial target for
	// paired devices so unauthenticated discovery packets can never redirect
	// a paired auto-dial. Zero means unknown (fall back to 1716).
	lastPort int

	// discoveryIP/discoveryPort remember where a device was last seen
	// announcing itself (UDP/mDNS), even if we never opened a TCP
	// connection to it. Used to dial on explicit user request
	// (e.g. `kcd pair <id>`) without auto-dialling strangers.
	discoveryIP   net.IP
	discoveryPort int

	// pairDialRequested is the one-shot outbound dial trigger for an explicit
	// `kcd pair <id>` request. It is consumed by the first discovery
	// announcement after the request so the pair request can be delivered.
	pairDialRequested atomic.Bool

	// pairIntentUntil is the Unix-nano deadline until which an explicit pair
	// intent keeps a connection alive. Unlike pairDialRequested (consumed on
	// first sighting), the intent survives dial/connect cycles until pairing
	// starts, is rejected, succeeds, or the deadline (pairDialIntentTTL)
	// expires — so a slow phone-side accept can't downgrade into an
	// ephemeral-close flap.
	pairIntentUntil atomic.Int64

	// lastDiscoveryDial is when onDeviceFound last spawned a dial for this
	// device. It throttles sighting-triggered redials so announcements
	// (or a spoofed broadcast storm) can't cause a dial per packet.
	lastDiscoveryDial time.Time

	// ephemeralDialed marks that this device already received its one
	// ephemeral discovery dial for the current unpaired era. Ephemeral
	// dials let a stranger complete the TCP identity exchange (so both
	// sides list each other) without staying connected: the next sighting
	// closes the socket again while the device is still unpaired. The
	// marker is cleared when the device is explicitly unpaired/rejected,
	// making it eligible again. Paired devices and pairing mode bypass it.
	ephemeralDialed bool

	conn      *transport.Conn
	sendChan  chan *protocol.Packet // buffered 32
	done      chan struct{}
	closeOnce sync.Once

	BatteryCharge int
	IsCharging    bool

	// batterySeen marks that at least one kdeconnect.battery packet was
	// received. Until then the zero values above are not measurements —
	// they must not be published (a fresh pair would otherwise report a
	// stable, bogus 0% that no later packet corrects at steady charge).
	batterySeen bool
	// lastBatteryAt is when the last battery packet arrived, so clients
	// can apply their own staleness rules (mirrors mediaAgeMs).
	lastBatteryAt time.Time

	mu sync.RWMutex

	// reconnecting is an atomic flag preventing multiple concurrent
	// auto-reconnect goroutines for this device.
	reconnecting atomic.Bool

	// reconnectAttempt persists the auto-reconnect backoff counter across
	// disconnect cycles. A connection that flaps (drops shortly after a
	// successful dial) keeps the counter so the backoff escalates instead of
	// resetting to the 2s floor; a stable connection resets it on drop.
	reconnectAttempt int

	// connectStarted is when the most recent connection was established,
	// used to detect flaps (connections that die too quickly to count as
	// genuinely stable).
	connectStarted time.Time

	// pluginDispatch routes incoming packets to registered plugins
	pluginDispatch func(ctx context.Context, dev *Device, pkt *protocol.Packet) bool
	onConnect      func(dev *Device)
	onDisconnect   func(dev *Device)

	logger *zap.Logger
	bus    *events.Bus
}

// NewDevice creates a new disconnected device instance.
// New devices start as Unpaired (not Unknown) so listings are unambiguous.
func NewDevice(id, name, dtype string, logger *zap.Logger) *Device {
	return &Device{
		id:       id,
		name:     name,
		Type:     dtype,
		state:    StateUnpaired,
		sendChan: make(chan *protocol.Packet, 32),
		done:     make(chan struct{}),
		logger:   logger.With(zap.String("device_id", id)),
	}
}

// SetBus sets the event bus for the device.
func (d *Device) SetBus(bus *events.Bus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bus = bus
}

// Connect establishes a connection for the device and starts the reader and writer loops.
func (d *Device) Connect(ctx context.Context, conn *transport.Conn, dispatch func(context.Context, *Device, *protocol.Packet) bool, onConnect func(*Device), onDisconnect func(*Device)) {
	d.mu.Lock()
	if d.conn != nil {
		_ = d.conn.Close()
	}
	d.conn = conn
	d.pluginDispatch = dispatch
	d.onConnect = onConnect
	d.onDisconnect = onDisconnect

	// Renew the send channel on connect in case it was closed during disconnect.
	d.sendChan = make(chan *protocol.Packet, 32)
	d.done = make(chan struct{})
	d.closeOnce = sync.Once{}
	bus := d.bus
	d.mu.Unlock()

	d.logger.Info("device connected", zap.String("remote_addr", conn.RemoteAddr().String()))
	if bus != nil {
		bus.Publish(events.TypeDeviceConnected, d.id, map[string]interface{}{
			"name": d.name,
			"type": d.Type,
		})
	}

	if d.onConnect != nil {
		d.onConnect(d)
	}

	// Cache the remote IP before the loops start so it is available
	// after Disconnect() sets d.conn to nil (used by auto-reconnect).
	if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		d.mu.Lock()
		d.lastIP = tcpAddr.IP
		d.mu.Unlock()
	}

	// Record when the connection was established so a quick drop can be
	// distinguished from a genuinely stable connection.
	d.mu.Lock()
	d.connectStarted = time.Now()
	d.mu.Unlock()

	go d.readLoop(ctx, conn)
	go d.writerLoop(ctx, conn)
}

// Disconnect terminates the connection and stops the loops.
func (d *Device) Disconnect() {
	d.mu.Lock()
	d.lastSeen = time.Now()

	if d.conn == nil {
		d.mu.Unlock()
		return
	}

	d.logger.Info("device disconnected")
	_ = d.conn.Close()
	d.conn = nil

	// Capture these to call outside the lock to prevent deadlocks!
	onDisc := d.onDisconnect
	bus := d.bus

	d.closeOnce.Do(func() {
		if d.done != nil {
			close(d.done)
		}
	})
	d.mu.Unlock()

	// External calls must happen outside the mutex
	if onDisc != nil {
		onDisc(d)
	}
	if bus != nil {
		bus.Publish(events.TypeDeviceDisconnected, d.id, nil)
	}
}

// IsConnected returns whether the device currently has an active connection.
func (d *Device) IsConnected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.conn != nil
}

// disconnectConn disconnects only if the provided connection matches the current one.
// This prevents old readLoops from terminating new connections.
func (d *Device) disconnectConn(conn *transport.Conn) {
	d.mu.Lock()

	// Only disconnect if this conn is still the active one
	if d.conn != conn {
		d.mu.Unlock()
		d.logger.Debug("ignoring disconnect from old connection")
		return
	}

	d.lastSeen = time.Now()
	d.logger.Info("device disconnected")
	_ = d.conn.Close()
	d.conn = nil

	// Capture callbacks to execute outside the lock
	onDisc := d.onDisconnect
	bus := d.bus

	d.closeOnce.Do(func() {
		if d.done != nil {
			close(d.done)
		}
	})
	d.mu.Unlock()

	// External calls must happen outside the mutex to prevent deadlocks
	if onDisc != nil {
		onDisc(d)
	}
	if bus != nil {
		bus.Publish(events.TypeDeviceDisconnected, d.id, nil)
	}
}

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
func (d *Device) ID() string {
	return d.id
}
func (d *Device) Name() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.name
}
func (d *Device) SetName(n string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.name = n
}
func (d *Device) State() PairingState {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.state
}
func (d *Device) SetState(s PairingState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state = s
}
func (d *Device) LastSeen() time.Time {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.lastSeen
}
func (d *Device) SetLastSeen(t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastSeen = t
}

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
func (d *Device) RequestPairDial() {
	d.pairDialRequested.Store(true)
	d.pairIntentUntil.Store(time.Now().Add(pairDialIntentTTL).UnixNano())
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
