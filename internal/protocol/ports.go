package protocol

// Well-known KDE Connect ports: DefaultTCPPort serves TCP, UDP discovery,
// and identity tcpPort; the side-channel range bounds file transfers. All
// are user-overridable — reference config, default to these only.
const (
	DefaultTCPPort = 1716

	DefaultSidechannelPortMin = 1739
	DefaultSidechannelPortMax = 1764
)
