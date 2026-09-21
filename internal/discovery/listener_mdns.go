package discovery

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/libp2p/zeroconf/v2"
)

// AdvertiseMDNS registers the local identity as _kdeconnect._udp until
// ctx ends. Unlike UDP broadcast this is responder-only (it wakes on
// incoming queries), so it stays up for the daemon lifetime at negligible
// idle cost and gives phones a standing discovery path even while UDP
// broadcast is stopped.
func AdvertiseMDNS(ctx context.Context, identityPacket *protocol.Packet, logger log.Logger) {
	var idBody protocol.IdentityBody
	if err := json.Unmarshal(identityPacket.Body, &idBody); err != nil {
		logger.Warn("failed to parse identity for mDNS", log.Error(err))
		return
	}
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
		logger.Warn("failed to register mDNS service", log.Error(err))
		return
	}
	go func() {
		<-ctx.Done()
		server.Shutdown()
		logger.Info("mDNS service shut down")
	}()
}

// runMdnsDiscovery browses for peer _kdeconnect._udp services and feeds
// sightings to onDeviceFound as synthetic identity packets. It is a method
// on Listener (kept apart from the UDP Run loop) so both transports share
// the same callback and self-filtering.
func (l *Listener) runMdnsDiscovery(ctx context.Context) {
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

	if err := zeroconf.Browse(ctx, "_kdeconnect._udp", "local.", entries); err != nil {
		l.logger.Warn("failed to browse mDNS", log.Error(err))
	}
}
