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
	// lastPort is the tcpPort the peer last advertised over the authenticated
	// (post-TLS) identity exchange. Used with lastIP as the dial target for
	// paired devices so unauthenticated discovery packets can never redirect
	// a paired auto-dial. Zero means unknown (fall back to the configured
	// tcp_port, protocol.DefaultTCPPort by default).
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

	// lastConnect marks the last completed handshake; new handshakes
	// inside reconnectCooldown are refused to starve duplicate bursts.
	lastConnect time.Time
	// lastSightedIP remembers the previous discovery sighting so only
	// confirmed roams reset the reconnect backoff (see NoteSighting).
	lastSightedIP net.IP
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
		logger:   logger.With(log.String("device_id", id)),
	}
}

// SetBus sets the event bus for the device.
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
