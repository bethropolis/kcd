package device

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
	"go.uber.org/zap"
)

// reconnectCooldown refuses new handshakes this long after a completed
// one, so post-roam bursts can't complete near-simultaneously and churn
// the peer's duplicate resolution. Reference stacks rate-limit the same
// way (desktop 500ms, Android MILLIS_DELAY_BETWEEN_CONNECTIONS_TO_SAME_DEVICE
// 1000ms); 1s matches upstream Android.
const reconnectCooldown = 1 * time.Second

// Connect establishes a connection for the device and starts the reader and writer loops.
// A new authenticated connection immediately replaces any existing one
// (matching LanDeviceLink::reset / LanLink.reset). The old socket is
// closed; its readLoop will exit and disconnectConn will ignore it
// because d.conn no longer points at it.
func (d *Device) Connect(ctx context.Context, conn *transport.Conn, dispatch func(context.Context, *Device, *protocol.Packet) bool, onConnect func(*Device), onDisconnect func(*Device)) {
	d.mu.Lock()
	d.pluginDispatch = dispatch
	d.onConnect = onConnect
	d.onDisconnect = onDisconnect

	var oldConn *transport.Conn
	var oldAddr, newAddr string
	if d.conn != nil {
		oldConn = d.conn
		oldDone := d.done
		oldAddr = oldConn.RemoteAddr().String()
		newAddr = conn.RemoteAddr().String()
		// Close the old done so its writerLoop exits; the new
		// writerLoop will own the fresh channel.
		d.closeOnce.Do(func() {
			if oldDone != nil {
				close(oldDone)
			}
		})
	}

	d.conn = conn

	// Renew the send channel on (re)connect in case it was closed during disconnect.
	d.sendChan = make(chan *protocol.Packet, 32)
	d.done = make(chan struct{})
	d.closeOnce = sync.Once{}
	bus := d.bus
	d.mu.Unlock()

	if oldConn != nil {
		_ = oldConn.Close()
		d.logger.Debug("replacing superseded connection",
			zap.String("old_addr", oldAddr),
			zap.String("new_addr", newAddr))
	}

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

	// connectStarted distinguishes quick drops from stable connections,
	// and doubles as the duplicate-cooldown clock (see InCooldown).
	d.mu.Lock()
	d.connectStarted = time.Now()
	d.lastConnect = d.connectStarted
	d.mu.Unlock()

	go d.readLoop(ctx, conn)
	go d.writerLoop(ctx)
}

// Disconnect terminates the session and stops the loops.
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
	d.lastConnect = time.Time{}

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

// disconnectConn handles a session's death. If the conn that died is no
// longer the current d.conn (it was superseded by a newer authenticated
// connection via Connect), the event is ignored silently — matching
// LanDeviceLink::reset's `if (m_socket == socket)` guard. Only the
// current preferred session's death triggers the full disconnect.
func (d *Device) disconnectConn(conn *transport.Conn) {
	d.mu.Lock()

	if d.conn != conn {
		d.mu.Unlock()
		d.logger.Debug("ignoring disconnect from superseded connection")
		return
	}

	d.lastSeen = time.Now()
	d.logger.Info("device disconnected")
	_ = d.conn.Close()
	d.conn = nil
	d.lastConnect = time.Time{}

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
