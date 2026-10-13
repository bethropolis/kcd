package device

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
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
	// lastPort pairs with lastIP as the dial target, from the authenticated
	// exchange: unauthenticated packets can never redirect a paired auto-dial.
	lastPort int

	// discoveryIP/discoveryPort is the last announcement source, kept without
	// any connection so explicit pair requests can dial without auto-dial.
	discoveryIP   net.IP
	discoveryPort int

	// pairDialRequested is the one-shot dial trigger for explicit pair, consumed
	// by the first announcement (unlike pairIntentUntil, which persists).
	pairDialRequested atomic.Bool

	// pairIntentUntil keeps an explicit pair intent alive across dial/connect
	// cycles, so a slow phone-side accept never downgrades into close flap.
	pairIntentUntil atomic.Int64

	// lastDiscoveryDial marks the last sighting-triggered dial, throttling
	// redials so announcement storms can't dial per packet.
	lastDiscoveryDial time.Time

	// ephemeralDialed records the one discovery dial per unpaired era: enough
	// for both sides to list each other, closed again on next sighting.
	ephemeralDialed bool

	conn      *transport.Conn
	sendChan  chan *protocol.Packet // buffered 32
	done      chan struct{}
	closeOnce sync.Once

	// lastConnect marks the last completed handshake; handshakes inside
	// reconnectCooldown are refused so duplicate bursts cannot starve a retry.
	lastConnect time.Time
	// lastSightedIP is the previous sighting, so only confirmed roams reset
	// the reconnect backoff (see NoteSighting).
	lastSightedIP net.IP
	BatteryCharge int
	IsCharging    bool

	// batterySeen marks arrival of a real reading: until then the zeroes are
	// not measurements and must not publish as a bogus stable 0%.
	batterySeen bool
	// lastBatteryAt is when the last battery packet arrived, so clients
	// can apply their own staleness rules (mirrors mediaAgeMs).
	lastBatteryAt time.Time

	mu sync.RWMutex

	// reconnecting is an atomic flag preventing multiple concurrent
	// auto-reconnect goroutines for this device.
	reconnecting atomic.Bool

	// reconnectWake nudges a parked reconnect loop, buffered-1 so pokes never
	// block discovery; coalesced bursts mean "check now", not N dials.
	reconnectWake chan struct{}

	// reconnectAttempt persists the backoff counter across disconnects: flaps
	// escalate it, stable connections reset it on drop.
	reconnectAttempt int

	// connectStarted marks the latest connection, detecting flaps (deaths too
	// quick to count as stable).
	connectStarted time.Time

	// pluginDispatch routes incoming packets to registered plugins
	pluginDispatch func(ctx context.Context, dev *Device, pkt *protocol.Packet) bool
	onConnect      func(dev *Device)
	onDisconnect   func(dev *Device)

	logger log.Logger
	bus    *events.Bus
}

// NewDevice creates a new disconnected device instance.
// New devices start as Unpaired (not Unknown) so listings are unambiguous.
func NewDevice(id, name, dtype string, logger log.Logger) *Device {
	return &Device{
		id:       id,
		name:     name,
		Type:     dtype,
		state:    StateUnpaired,
		sendChan: make(chan *protocol.Packet, 32),
		done:     make(chan struct{}),
		// A send on a nil channel blocks forever, so the select takes default:
		// nil-safe, and zero-value Devices work in tests.
		reconnectWake: make(chan struct{}, 1),
		logger:        logger.With(log.String("device_id", id)),
	}
}

func (d *Device) SetBus(bus *events.Bus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bus = bus
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
	// Wake a parked reconnect loop so unpair takes effect now, not at the
	// next timer (or never, past the stale horizon); it re-checks and exits.
	if s == StateUnpaired {
		d.PokeReconnect()
	}
}

// PokeReconnect wakes the reconnect loop to re-check now; non-blocking and
// nil-safe, with bursts coalescing into a single wakeup.
func (d *Device) PokeReconnect() {
	select {
	case d.reconnectWake <- struct{}{}:
	default:
	}
}

// ReconnectWake exposes the wake channel for the auto-reconnect loop's
// select. A nil channel (zero-value Device) blocks forever — safe.
func (d *Device) ReconnectWake() <-chan struct{} {
	return d.reconnectWake
}

// Reconnecting reports whether an auto-reconnect goroutine is running.
func (d *Device) Reconnecting() bool {
	return d.reconnecting.Load()
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
