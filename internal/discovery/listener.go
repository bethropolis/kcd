package discovery

import (
	"context"
	"encoding/json"
	"net"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// Listener listens for UDP identity packets from other devices.
type Listener struct {
	port          int
	localDeviceID string
	onDeviceFound func(ip net.IP, tcpPort int, identity *protocol.Packet)
	logger        log.Logger
}

// NewListener creates a UDP discovery listener.
func NewListener(port int, localDeviceID string, callback func(ip net.IP, tcpPort int, identity *protocol.Packet), logger log.Logger) *Listener {
	return &Listener{
		port:          port,
		localDeviceID: localDeviceID,
		onDeviceFound: callback,
		logger:        logger.With(log.String("component", "udp-listener")),
	}
}

// Run starts the UDP listener loop to parse incoming discovery broadcasts.
func (l *Listener) Run(ctx context.Context) {
	// mDNS Discovery
	go l.runMdnsDiscovery(ctx)

	addr := &net.UDPAddr{Port: l.port}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		l.logger.Error("failed to listen on udp", log.Int("port", l.port), log.Error(err))
		return
	}
	defer conn.Close()

	// 8KB buffer shouldn't be exceeded by an identity packet
	buf := make([]byte, 8192)

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return // clean exit on context cancel
			}
			l.logger.Debug("read udp error", log.Error(err))
			continue
		}

		if n >= len(buf) || n == 0 {
			continue // ignore giant/empty packets
		}

		pkt := protocol.AcquirePacket()
		if err := json.Unmarshal(buf[:n], pkt); err != nil {
			protocol.ReleasePacket(pkt)
			continue
		}

		if pkt.Type != protocol.TypeIdentity {
			protocol.ReleasePacket(pkt)
			continue
		}

		var identity protocol.IdentityBody
		if err := json.Unmarshal(pkt.Body, &identity); err != nil {
			protocol.ReleasePacket(pkt)
			continue
		}

		// Don't connect to ourselves
		if identity.DeviceID == l.localDeviceID {
			protocol.ReleasePacket(pkt)
			continue
		}

		if l.onDeviceFound != nil {
			l.onDeviceFound(remoteAddr.IP, identity.TCPPort, pkt)
		}
		protocol.ReleasePacket(pkt)
	}
}
