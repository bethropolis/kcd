package transport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"

	"github.com/bethropolis/kcd/internal/protocol"
)

// bufferedConn wraps a net.Conn and an io.Reader. It reads from the reader first,
// and once the reader is exhausted, it reads from the underlying net.Conn.
// This is used to replay unread bytes that were buffered during plaintext reading.
type bufferedConn struct {
	net.Conn
	r io.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

// ReadPlaintextPacket reads exactly one newline-terminated packet from a net.Conn.
// It uses a bufio.Reader to avoid 1-byte read overhead, and returns a new net.Conn
// that replays any buffered bytes that belong to the subsequent TLS handshake.
func ReadPlaintextPacket(conn net.Conn) (*protocol.Packet, net.Conn, error) {
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, conn, err
	}

	if len(line) > protocol.MaxPacketSize {
		return nil, conn, fmt.Errorf("transport: plaintext packet too large")
	}

	pkt := protocol.AcquirePacket()
	if err := json.Unmarshal(line, pkt); err != nil {
		protocol.ReleasePacket(pkt)
		return nil, conn, fmt.Errorf("transport: unmarshal plaintext packet: %w", err)
	}

	// Calculate remaining buffered bytes
	buffered := reader.Buffered()
	if buffered > 0 {
		buf := make([]byte, buffered)
		_, err := io.ReadFull(reader, buf)
		if err != nil {
			return nil, conn, fmt.Errorf("transport: failed to save buffered bytes: %w", err)
		}
		// Return a wrapped connection that replays the buffered bytes
		replayReader := io.MultiReader(bytes.NewReader(buf), conn)
		return pkt, &bufferedConn{Conn: conn, r: replayReader}, nil
	}

	return pkt, conn, nil
}

// WritePlaintextPacket writes a packet directly to a net.Conn without TLS.
func WritePlaintextPacket(conn net.Conn, p *protocol.Packet) error {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("transport: marshal plaintext packet: %w", err)
	}
	data = append(data, '\n')
	_, err = conn.Write(data)
	return err
}
