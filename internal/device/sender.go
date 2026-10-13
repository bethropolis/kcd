package device

import (
	"crypto/x509"
	"net"

	"github.com/bethropolis/kcd/internal/protocol"
)

// Sender is a KDE Connect device that can receive packets, letting plugins
// interact without knowing device internals.
type Sender interface {
	ID() string
	Name() string
	SetName(name string)
	State() PairingState
	SetState(state PairingState)

	Send(p *protocol.Packet) error
	IsConnected() bool
	RemoteIP() net.IP
	PeerCert() *x509.Certificate

	HasCapability(cap string) bool

	UpdateBattery(charge int, charging bool)
	GetBattery() (int, bool)
}
