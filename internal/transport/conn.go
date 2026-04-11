// Package transport handles the TLS TCP connection and packet framing layer.
package transport

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"net"

	"github.com/bethropolis/kcd/internal/protocol"
)

// Conn wraps a tls.Conn to provide packet-level reads and writes.
// It maintains a single bufio.Reader to avoid per-packet allocations.
type Conn struct {
	tlsConn *tls.Conn
	reader  *bufio.Reader
	addr    net.Addr
}

// NewConn constructs a new Conn from a tls.Conn.
func NewConn(tc *tls.Conn) *Conn {
	return &Conn{
		tlsConn: tc,
		reader:  bufio.NewReader(tc),
		addr:    tc.RemoteAddr(),
	}
}

// ReadPacket reads a single KDE Connect packet from the TLS connection.
func (c *Conn) ReadPacket() (*protocol.Packet, error) {
	return protocol.ReadPacket(c.reader)
}

// WritePacket writes a single KDE Connect packet to the TLS connection.
func (c *Conn) WritePacket(p *protocol.Packet) error {
	return protocol.WritePacket(c.tlsConn, p)
}

// Close closes the underlying TLS connection.
func (c *Conn) Close() error {
	return c.tlsConn.Close()
}

// PeerCert returns the validated client/server certificate presented by the peer.
// If no certificate was presented (which shouldn't happen with proper tls.Config), it returns nil.
func (c *Conn) PeerCert() *x509.Certificate {
	state := c.tlsConn.ConnectionState()
	if len(state.PeerCertificates) > 0 {
		return state.PeerCertificates[0]
	}
	return nil
}

// RemoteAddr returns the underlying network remote address.
func (c *Conn) RemoteAddr() net.Addr {
	return c.addr
}
