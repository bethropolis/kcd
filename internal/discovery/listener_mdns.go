package discovery

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/libp2p/zeroconf/v2"
)

// AdvertiseMDNS registers the local identity until ctx ends. Responder-only,
// so it stays up for the daemon lifetime at negligible idle cost.
func AdvertiseMDNS(ctx context.Context, identityPacket *protocol.Packet, logger log.Logger) {
	var idBody protocol.IdentityBody
	if err := json.Unmarshal(identityPacket.Body, &idBody); err != nil {
		logger.Warn("failed to parse identity for mDNS", log.Error(err))
		return
	}
	ifaces := mdnsInterfaces()
	if len(ifaces) == 0 {
		logger.Warn("no eligible interfaces for mDNS advertisement")
		return
	}
	server, err := zeroconf.Register(
		idBody.DeviceID,
		"_kdeconnect._udp",
		"local.",
		idBody.TCPPort,
		[]string{
			"id=" + idBody.DeviceID,
			"name=" + idBody.DeviceName,
			"type=" + idBody.DeviceType,
			"protocol=8",
		},
		ifaces,
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

// RunMdnsDiscovery feeds peer sightings to onDeviceFound, running only
// while broadcast has owners: zeroconf's tickers break zero-idle state.
func (l *Listener) RunMdnsDiscovery(ctx context.Context) {
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

			if !protocol.ValidDeviceID(deviceId) || deviceId == l.localDeviceID {
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

	ifaces := mdnsInterfaces()
	if len(ifaces) == 0 {
		l.logger.Warn("no eligible interfaces for mDNS browse")
		return
	}
	if err := zeroconf.Browse(ctx, "_kdeconnect._udp", "local.", entries, zeroconf.SelectIfaces(ifaces)); err != nil {
		l.logger.Warn("failed to browse mDNS", log.Error(err))
	}
}

func mdnsInterfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var eligible []net.Interface
	for _, iface := range all {
		if iface.Flags&(net.FlagUp|net.FlagMulticast) != (net.FlagUp|net.FlagMulticast) || excludedMDNSInterface(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err == nil && ip.IsPrivate() {
				eligible = append(eligible, iface)
				break
			}
		}
	}
	return eligible
}

func excludedMDNSInterface(name string) bool {
	for _, prefix := range []string{"docker", "br-", "veth", "virbr", "lxc", "tun", "wg"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return name == "lo"
}
