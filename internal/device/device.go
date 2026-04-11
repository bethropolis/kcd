package device

import (
	"context"
	"crypto/x509"
	"net"
	"sync"
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

	conn      *transport.Conn
	sendChan  chan *protocol.Packet // buffered 32
	done      chan struct{}
	closeOnce sync.Once

	BatteryCharge int
	IsCharging    bool

	mu sync.RWMutex

	// pluginDispatch routes incoming packets to registered plugins
	pluginDispatch func(ctx context.Context, dev *Device, pkt *protocol.Packet)
	onConnect      func(dev *Device)
	onDisconnect   func(dev *Device)

	logger *zap.Logger
	bus    *events.Bus
}

// NewDevice creates a new disconnected device instance.
func NewDevice(id, name, dtype string, logger *zap.Logger) *Device {
	return &Device{
		id:       id,
		name:     name,
		Type:     dtype,
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

// Send attempts to enqueue a packet for delivery to the device.
// If the send channel is full, the packet is dropped to avoid blocking.
func (d *Device) Send(p *protocol.Packet) error {
	d.mu.RLock()
	connected := d.conn != nil
	sendChan := d.sendChan
	done := d.done
	d.mu.RUnlock()

	if !connected {
		protocol.ReleasePacket(p)
		return nil // silently drop if not connected
	}

	select {
	case <-done:
		protocol.ReleasePacket(p)
		return nil
	case sendChan <- p:
		return nil
	default:
		d.logger.Warn("send channel full, dropping packet", zap.String("type", p.Type))
		protocol.ReleasePacket(p)
		return nil
	}
}

// Connect establishes a connection for the device and starts the reader and writer loops.
func (d *Device) Connect(ctx context.Context, conn *transport.Conn, dispatch func(context.Context, *Device, *protocol.Packet), onConnect func(*Device), onDisconnect func(*Device)) {
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

	go d.readLoop(ctx, conn)
	go d.writerLoop(ctx, conn)
}

// Disconnect terminates the connection and stops the loops.
func (d *Device) Disconnect() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.lastSeen = time.Now()

	if d.conn != nil {
		d.logger.Info("device disconnected")
		_ = d.conn.Close()
		d.conn = nil

		if d.onDisconnect != nil {
			d.onDisconnect(d)
		}

		if d.bus != nil {
			d.bus.Publish(events.TypeDeviceDisconnected, d.id, nil)
		}
	}

	d.closeOnce.Do(func() {
		if d.done != nil {
			close(d.done)
		}
	})
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
	defer d.mu.Unlock()

	// Only disconnect if this conn is still the active one
	if d.conn != conn {
		d.logger.Debug("ignoring disconnect from old connection")
		return
	}

	d.lastSeen = time.Now()

	d.logger.Info("device disconnected")
	_ = d.conn.Close()
	d.conn = nil

	if d.onDisconnect != nil {
		d.onDisconnect(d)
	}

	if d.bus != nil {
		d.bus.Publish(events.TypeDeviceDisconnected, d.id, nil)
	}

	d.closeOnce.Do(func() {
		if d.done != nil {
			close(d.done)
		}
	})
}

// readLoop reads packets from the network and routes them.
func (d *Device) readLoop(ctx context.Context, conn *transport.Conn) {
	d.mu.RLock()
	dispatch := d.pluginDispatch
	d.mu.RUnlock()

	if conn == nil {
		return
	}

	defer d.disconnectConn(conn)

	for {
		if ctx.Err() != nil {
			return
		}

		pkt, err := conn.ReadPacket()
		if err != nil {
			d.logger.Debug("read packet error (disconnecting)", zap.Error(err))
			return
		}

		if dispatch != nil {
			if d.State() != StatePaired && pkt.Type != protocol.TypeIdentity && pkt.Type != protocol.TypePair {
				d.logger.Debug("dropping packet from unpaired device", zap.String("type", pkt.Type))
			} else {
				dispatch(ctx, d, pkt)
			}
		}

		// Don't leak memory; return the packet to pool after dispatch returns.
		// Handlers shouldn't keep references to the original packet struct.
		protocol.ReleasePacket(pkt)
	}
}

// writerLoop drains the send channel and writes packets to the network.
func (d *Device) writerLoop(ctx context.Context, conn *transport.Conn) {
	d.mu.RLock()
	sendChan := d.sendChan
	done := d.done
	d.mu.RUnlock()

	if conn == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case pkt := <-sendChan:
			if err := conn.WritePacket(pkt); err != nil {
				d.logger.Debug("write packet error", zap.Error(err))
				return // write failed -> drop out, readLoop will detect disconnect soon
			}
		}
	}
}

// UpdateBattery updates the battery state of the device.
func (d *Device) UpdateBattery(charge int, charging bool) {
	d.mu.Lock()
	d.BatteryCharge = charge
	d.IsCharging = charging
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
