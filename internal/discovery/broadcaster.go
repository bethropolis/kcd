package discovery

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// Broadcaster sends identity packets over UDP to advertise the local device.
type Broadcaster struct {
	identityPacket *protocol.Packet
	port           int
	interval       time.Duration
	idleInterval   time.Duration
	logger         log.Logger
	resetCh        <-chan struct{}
}

// NewBroadcaster creates a UDP discovery broadcaster targeting port.
func NewBroadcaster(identity *protocol.Packet, port int, interval time.Duration, logger log.Logger) *Broadcaster {
	return &Broadcaster{
		identityPacket: identity,
		port:           port,
		interval:       interval,
		logger:         logger.With(log.String("component", "broadcaster")),
	}
}

const maxReconnectInterval = 15 * time.Minute

// Run sends identity to broadcast with exponential backoff; shouldReduce
// retains the quiet interval when all connected devices are idle.
func (b *Broadcaster) Run(ctx context.Context, shouldReduce func() bool) {
	normalInterval := b.interval
	reducedInterval := b.idleInterval
	if reducedInterval <= 0 {
		reducedInterval = defaultIdleInterval
	}

	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		b.logger.Error("failed to listen for udp broadcast", log.Error(err))
		return
	}
	defer conn.Close()

	data, err := json.Marshal(b.identityPacket)
	if err != nil {
		b.logger.Error("failed to marshal identity packet", log.Error(err))
		return
	}
	data = append(data, '\n')

	timer := time.NewTimer(0) // Fire immediately on start
	defer timer.Stop()
	currentInterval := normalInterval

	for {
		select {
		case <-ctx.Done():
			return
		case <-b.resetCh:
			currentInterval = normalInterval
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(0)
		case <-timer.C:
			// 1. Attempt global broadcast
			globalAddr := &net.UDPAddr{IP: net.IPv4bcast, Port: b.port}
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
								conn.WriteToUDP(data, &net.UDPAddr{IP: bcast, Port: b.port})
							}
						}
					}
				}
			}

			nextInterval := currentInterval
			if shouldReduce != nil && shouldReduce() {
				nextInterval = reducedInterval
			} else {
				if currentInterval < maxReconnectInterval {
					currentInterval *= 2
					if currentInterval > maxReconnectInterval {
						currentInterval = maxReconnectInterval
					}
				}
			}
			timer.Reset(nextInterval)
		}
	}
}
