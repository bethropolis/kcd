package protocol

// Well-known KDE Connect ports: DefaultTCPPort serves TCP, DefaultDiscoveryPort
// serves UDP discovery; the side-channel range bounds file transfers.
const (
	DefaultTCPPort       = 1716
	DefaultDiscoveryPort = 1716
	DiscoveryPort        = DefaultDiscoveryPort

	TCPPortMin = 1716
	TCPPortMax = 1764

	DefaultSidechannelPortMin = 1739
	DefaultSidechannelPortMax = 1764
)
