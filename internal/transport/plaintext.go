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

// bufferedConn replays reader bytes before the underlying conn: unread
// plaintext-read leftovers belonging to the TLS handshake.
type bufferedConn struct {
	net.Conn
	r io.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

// ReadPlaintextPacket reads one newline-terminated packet via bufio,
// returning a conn replaying buffered bytes into the TLS handshake.
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
