package discovery

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// Broadcaster sends identity packets over UDP to advertise the local device.
type Broadcaster struct {
	identityPacket *protocol.Packet
	interval       time.Duration
	idleInterval   time.Duration
	logger         *zap.Logger
}

// NewBroadcaster creates a UDP discovery broadcaster.
func NewBroadcaster(identity *protocol.Packet, interval time.Duration, logger *zap.Logger) *Broadcaster {
	return &Broadcaster{
		identityPacket: identity,
		interval:       interval,
		logger:         logger.With(zap.String("component", "broadcaster")),
	}
}

// Run periodically sends the identity packet to 255.255.255.255:1716.
// If shouldReduce is provided and returns true, the broadcast frequency
// is reduced to 60 seconds to save CPU and network resources while idle.
// (mDNS advertisement is no longer tied to this loop — see AdvertiseMDNS.)
func (b *Broadcaster) Run(ctx context.Context, shouldReduce func() bool) {
	normalInterval := b.interval
	reducedInterval := b.idleInterval
	if reducedInterval <= 0 {
		reducedInterval = 60 * time.Second
	}

	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		b.logger.Error("failed to listen for udp broadcast", zap.Error(err))
		return
	}
	defer conn.Close()

	data, err := json.Marshal(b.identityPacket)
	if err != nil {
		b.logger.Error("failed to marshal identity packet", zap.Error(err))
		return
	}
	data = append(data, '\n')

	timer := time.NewTimer(0) // Fire immediately on start
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			// 1. Attempt global broadcast
			globalAddr := &net.UDPAddr{IP: net.IPv4bcast, Port: 1716}
			conn.WriteToUDP(data, globalAddr)

			// 2. Attempt per-interface directed broadcast for multi-homed reliability
			ifaces, err := net.Interfaces()
			if err == nil {
				for _, iface := range ifaces {
					if iface.Flags&net.FlagBroadcast == 0 || iface.Flags&net.FlagUp == 0 {
						continue
					}
					addrs, err := iface.Addrs()
					if err != nil {
						continue
					}
					for _, a := range addrs {
						if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
							ip4 := ipnet.IP.To4()
							mask := ipnet.Mask[len(ipnet.Mask)-4:]
							if len(mask) == 4 {
								bcast := make(net.IP, 4)
								for i := 0; i < 4; i++ {
									bcast[i] = ip4[i] | ^mask[i]
								}
								conn.WriteToUDP(data, &net.UDPAddr{IP: bcast, Port: 1716})
							}
						}
					}
				}
			}

			nextInterval := normalInterval
			if shouldReduce != nil && shouldReduce() {
				nextInterval = reducedInterval
			}
			timer.Reset(nextInterval)
		}
	}
}
