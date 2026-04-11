package discovery

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/grandcat/zeroconf"
	"go.uber.org/zap"
)

// Broadcaster sends identity packets over UDP to advertise the local device.
type Broadcaster struct {
	identityPacket *protocol.Packet
	interval       time.Duration
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
func (b *Broadcaster) Run(ctx context.Context, shouldReduce func() bool) {
	// Register mDNS
	var idBody protocol.IdentityBody
	if err := json.Unmarshal(b.identityPacket.Body, &idBody); err == nil {
		server, err := zeroconf.Register(
			idBody.DeviceName,
			"_kdeconnect._udp",
			"local.",
			idBody.TCPPort,
			[]string{
				"id=" + idBody.DeviceID,
				"name=" + idBody.DeviceName,
				"type=" + idBody.DeviceType,
				"protocol=8",
			},
			nil,
		)
		if err != nil {
			b.logger.Warn("failed to register mDNS service", zap.Error(err))
		} else {
			go func() {
				<-ctx.Done()
				server.Shutdown()
				b.logger.Info("mDNS service shut down")
			}()
		}
	} else {
		b.logger.Warn("failed to parse identity for mDNS", zap.Error(err))
	}

	normalInterval := b.interval
	reducedInterval := 60 * time.Second

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

// Listener listens for UDP identity packets from other devices.
type Listener struct {
	port          int
	localDeviceID string
	onDeviceFound func(ip net.IP, tcpPort int, identity *protocol.Packet)
	logger        *zap.Logger
}

// NewListener creates a UDP discovery listener.
func NewListener(port int, localDeviceID string, callback func(ip net.IP, tcpPort int, identity *protocol.Packet), logger *zap.Logger) *Listener {
	return &Listener{
		port:          port,
		localDeviceID: localDeviceID,
		onDeviceFound: callback,
		logger:        logger.With(zap.String("component", "udp-listener")),
	}
}

// Run starts the UDP listener loop to parse incoming discovery broadcasts.
func (l *Listener) Run(ctx context.Context) {
	// mDNS Discovery
	go l.runMdnsDiscovery(ctx)

	addr := &net.UDPAddr{Port: l.port}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		l.logger.Error("failed to listen on udp", zap.Int("port", l.port), zap.Error(err))
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
			l.logger.Debug("read udp error", zap.Error(err))
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

func (l *Listener) runMdnsDiscovery(ctx context.Context) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		l.logger.Warn("failed to create mDNS resolver", zap.Error(err))
		return
	}

	entries := make(chan *zeroconf.ServiceEntry)
	go func(results <-chan *zeroconf.ServiceEntry) {
		for entry := range results {
			var deviceId, deviceName, deviceType string
			var protocolVersion int
			for _, txt := range entry.Text {
				key, val, ok := strings.Cut(txt, "=")
				if !ok {
					continue
				}
				switch key {
				case "id":
					deviceId = val
				case "name":
					deviceName = val
				case "type":
					deviceType = val
				case "protocol":
					protocolVersion, _ = strconv.Atoi(val)
				}
			}

			if deviceId == "" || deviceId == l.localDeviceID {
				continue
			}
			if len(entry.AddrIPv4) == 0 {
				continue
			}

			body := protocol.IdentityBody{
				DeviceID:        deviceId,
				DeviceName:      deviceName,
				DeviceType:      deviceType,
				ProtocolVersion: protocolVersion,
				TCPPort:         entry.Port,
			}
			pkt, err := protocol.NewPacket(protocol.TypeIdentity, body)
			if err != nil {
				continue
			}

			if l.onDeviceFound != nil {
				l.onDeviceFound(entry.AddrIPv4[0], entry.Port, pkt)
			}
			protocol.ReleasePacket(pkt)
		}
	}(entries)

	err = resolver.Browse(ctx, "_kdeconnect._udp", "local.", entries)
	if err != nil {
		l.logger.Warn("failed to browse mDNS", zap.Error(err))
	}
}
