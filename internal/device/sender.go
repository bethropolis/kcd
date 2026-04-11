package device

import (
	"crypto/x509"
	"net"

	"github.com/bethropolis/kcd/internal/protocol"
)

// Sender represents a KDE Connect device capable of receiving packets.
// This interface allows plugins to interact with a device without knowing
// its internal implementation details.
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
