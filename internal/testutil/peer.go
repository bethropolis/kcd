// Package testutil provides a mock KDE Connect peer for integration tests.
package testutil

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/protocol"
)

// MockPeer simulates a remote KDE Connect device.
type MockPeer struct {
	t         *testing.T
	tlsConfig *tls.Config
}

// NewMockPeer creates a new MockPeer.
func NewMockPeer(t *testing.T, tlsConfig *tls.Config) *MockPeer {
	t.Helper()
	return &MockPeer{t: t, tlsConfig: tlsConfig}
}

// Dial connects to the given TCP address, completes the plaintext identity
// exchange, then upgrades to TLS as the TLS server (KDE Connect rule: the
// TCP initiator acts as TLS server).
func (p *MockPeer) Dial(serverAddr string) net.Conn {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", serverAddr)
	if err != nil {
		p.t.Fatalf("mockpeer: dial %s: %v", serverAddr, err)
	}

	// Send plaintext identity
	identPkt, err := protocol.NewPacket(protocol.TypeIdentity, protocol.IdentityBody{
		DeviceID:        "mock-peer",
		DeviceName:      "Mock Peer",
		DeviceType:      "phone",
		ProtocolVersion: protocol.ProtocolVersion,
		TCPPort:         1716,
	})
	if err != nil {
		p.t.Fatalf("mockpeer: build identity: %v", err)
	}
	data, _ := json.Marshal(identPkt)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		p.t.Fatalf("mockpeer: write identity: %v", err)
	}

	// Upgrade to TLS as server (TCP initiator = TLS server per KDE Connect spec).
	tlsConn := tls.Server(conn, p.tlsConfig)
	handshakeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
		p.t.Fatalf("mockpeer: tls handshake: %v", err)
	}
	return tlsConn
}

// DialPipe creates an in-process net.Pipe pair, performs the identity exchange
// on the given server-side connection (serverConn), and returns the peer-side
// conn ready for SendPacket/ReadPacket.
// The caller is responsible for handling serverConn in a goroutine.
func (p *MockPeer) DialPipe() (peerConn net.Conn, serverConn net.Conn) {
	p.t.Helper()
	peerConn, serverConn = net.Pipe()
	return peerConn, serverConn
}

// SendPacket writes a packet as newline-delimited JSON on conn.
func (p *MockPeer) SendPacket(conn net.Conn, pkt *protocol.Packet) error {
	data, err := json.Marshal(pkt)
	if err != nil {
		return fmt.Errorf("mockpeer: marshal packet: %w", err)
	}
	data = append(data, '\n')
	_, err = conn.Write(data)
	return err
}

// ReadPacket reads one newline-delimited JSON packet from conn.
func (p *MockPeer) ReadPacket(conn net.Conn) (*protocol.Packet, error) {
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("mockpeer: read packet: %w", err)
	}
	pkt := protocol.AcquirePacket()
	if err := json.Unmarshal(line, pkt); err != nil {
		protocol.ReleasePacket(pkt)
		return nil, fmt.Errorf("mockpeer: unmarshal packet: %w", err)
	}
	return pkt, nil
}
