package device

import (
	"crypto/x509"
	"net"

	"github.com/bethropolis/kcd/internal/protocol"
)

// Peer represents a remote connected KDE Connect peer from the plugin's perspective.
type Peer interface {
	ID() string
	Name() string
	State() PairingState
	Send(p *protocol.Packet) error
	IsConnected() bool
	RemoteIP() net.IP
	PeerCert() *x509.Certificate
	HasCapability(cap string) bool
}

// Sender embeds Peer and adds mutable device state methods for internal daemon and pairing flows.
type Sender interface {
	Peer
	SetName(name string)
	SetState(state PairingState)
	UpdateBattery(charge int, charging bool)
	GetBattery() (int, bool)
}
