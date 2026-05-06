package device

import (
	"context"

	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
	"go.uber.org/zap"
)

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
