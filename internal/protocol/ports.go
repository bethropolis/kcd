package protocol

// Well-known KDE Connect ports. DefaultTCPPort is the control channel
// (TCP listener, UDP discovery, identity tcpPort); the side-channel range
// bounds inbound file-transfer listeners. All three are user-overridable
// via config — code must reference the configured value and fall back to
// these constants only for defaults.
const (
	DefaultTCPPort = 1716

	DefaultSidechannelPortMin = 1739
	DefaultSidechannelPortMax = 1764
)
