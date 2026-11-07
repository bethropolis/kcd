// Package transport handles the TLS TCP connection and packet framing layer.
package transport

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/bethropolis/kcd/internal/protocol"
)

// Conn wraps a tls.Conn to provide packet-level reads and writes.
// It maintains a single bufio.Reader to avoid per-packet allocations.
type Conn struct {
	tlsConn  *tls.Conn
	reader   *bufio.Reader
	addr     net.Addr
	writeBuf bytes.Buffer
	encoder  *json.Encoder
}

// NewConn constructs a new Conn from a tls.Conn.
func NewConn(tc *tls.Conn) *Conn {
	c := &Conn{
		tlsConn: tc,
		reader:  bufio.NewReader(tc),
		addr:    tc.RemoteAddr(),
	}
	c.encoder = json.NewEncoder(&c.writeBuf)
	c.encoder.SetEscapeHTML(false)
	return c
}

// ReadPacket reads a single KDE Connect packet from the TLS connection.
func (c *Conn) ReadPacket() (*protocol.Packet, error) {
	return protocol.ReadPacket(c.reader)
}

// WritePacket writes a single KDE Connect packet to the TLS connection.
func (c *Conn) WritePacket(p *protocol.Packet) error {
	c.writeBuf.Reset()
	if err := c.encoder.Encode(p); err != nil {
		return fmt.Errorf("protocol: marshal: %w", err)
	}
	if _, err := c.tlsConn.Write(c.writeBuf.Bytes()); err != nil {
		return fmt.Errorf("protocol: write: %w", err)
	}
	return nil
}

// Close closes the underlying TLS connection.
func (c *Conn) Close() error {
	return c.tlsConn.Close()
}

// SetWriteDeadline bounds the next WritePacket call: a stuck retransmit
// queue would block forever, and keepalive never probes a writing socket.
func (c *Conn) SetWriteDeadline(t time.Time) error {
	return c.tlsConn.SetWriteDeadline(t)
}

// SetReadDeadline bounds the next packet read, including post-TLS identity.
func (c *Conn) SetReadDeadline(t time.Time) error {
	return c.tlsConn.SetReadDeadline(t)
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
