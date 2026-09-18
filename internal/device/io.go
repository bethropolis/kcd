package device

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
)

// writeTimeout bounds one WritePacket call. LAN writes complete in
// milliseconds; anything slower is a half-open zombie whose retransmit
// queue never drains (TCP keepalive can't save it — it only probes idle
// sockets). Timing out routes the death through disconnectConn so the
// backoff and discovery repair paths can run.
const writeTimeout = 10 * time.Second

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
		d.logger.Warn("send channel full, dropping packet", log.String("type", p.Type))
		protocol.ReleasePacket(p)
		return nil
	}
}

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
			if strings.Contains(err.Error(), "protocol: unmarshal:") {
				d.logger.Warn("dropping malformed packet, keeping connection", log.Error(err))
				continue
			}
			d.logger.Debug("read packet error (disconnecting)", log.Error(err))
			return
		}

		if dispatch != nil {
			if d.State() != StatePaired && pkt.Type != protocol.TypeIdentity && pkt.Type != protocol.TypePair {
				d.logger.Debug("dropping packet from unpaired device", log.String("type", pkt.Type))
				protocol.ReleasePacket(pkt)
			} else if dispatch(ctx, d, pkt) {
				// Plugin completed within timeout — safe to recycle.
				protocol.ReleasePacket(pkt)
			}
			// If dispatch returned false (timeout), the background goroutine
			// still holds a reference to the packet. Skip ReleasePacket to
			// prevent a data race — the packet will be GC'd.
		} else {
			// Don't leak memory; return the packet to pool after dispatch returns.
			// Handlers shouldn't keep references to the original packet struct.
			protocol.ReleasePacket(pkt)
		}
	}
}

// writerLoop is the sole writer to device sockets. It resolves the
// preferred session per packet so promotions need no restart; write
// timeouts fail the session via disconnectConn, other write errors keep
// looping while readLoop routes the death.
func (d *Device) writerLoop(ctx context.Context) {
	d.mu.RLock()
	sendChan := d.sendChan
	done := d.done
	d.mu.RUnlock()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case pkt := <-sendChan:
			d.mu.RLock()
			conn := d.conn
			d.mu.RUnlock()
			if conn == nil {
				// Fully disconnected (done closes right behind this) —
				// drop rather than block the loop's exit.
				protocol.ReleasePacket(pkt)
				continue
			}
			d.logger.Debug("sending packet", log.String("type", pkt.Type))
			_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			err := conn.WritePacket(pkt)
			_ = conn.SetWriteDeadline(time.Time{})
			if err != nil {
				d.logger.Debug("write packet error", log.Error(err))
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					// Stuck socket: fail the session now instead of
					// blocking the writer forever behind Send-Q.
					d.logger.Info("write timed out, dropping zombie connection")
					protocol.ReleasePacket(pkt)
					d.disconnectConn(conn)
					continue
				}
			}
			protocol.ReleasePacket(pkt)
		}
	}
}
