// Package protocol implements the KDE Connect v8 packet framing.
// This package has zero external imports — stdlib only.
package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// MaxPacketSize is the maximum allowed line length for a single packet (4 MB).
// Lines exceeding this are rejected to prevent memory exhaustion.
const MaxPacketSize = 4 * 1024 * 1024

// Packet represents a single KDE Connect packet on the wire.
// Body is kept as json.RawMessage so the router never unmarshals plugin payloads.
type Packet struct {
	ID                  int64           `json:"id"`
	Type                string          `json:"type"`
	Body                json.RawMessage `json:"body"`
	PayloadSize         int64           `json:"payloadSize,omitempty"`
	PayloadTransferInfo *TransferInfo   `json:"payloadTransferInfo,omitempty"`
}

// TransferInfo holds the port for side-channel file transfers.
type TransferInfo struct {
	Port int `json:"port"`
}

// poolAcquires counts every call to AcquirePacket.
// poolMisses counts calls where sync.Pool had no object and allocated fresh.
// hits = acquires - misses.
var (
	poolAcquires atomic.Int64
	poolMisses   atomic.Int64
)

// packetPool avoids per-packet heap allocations on the hot read path.
var packetPool = sync.Pool{
	New: func() any {
		poolMisses.Add(1)
		return &Packet{}
	},
}

// AcquirePacket returns a zeroed Packet from the pool.
func AcquirePacket() *Packet {
	poolAcquires.Add(1)
	p := packetPool.Get().(*Packet)
	p.Reset()
	return p
}

// PoolStats returns packet pool usage counters for diagnostic logging.
// hits = acquires - misses.
// A miss rate above ~20% under steady load may indicate pool pressure
// from GC eviction.
func PoolStats() (acquires, misses int64) {
	return poolAcquires.Load(), poolMisses.Load()
}

// ReleasePacket returns a Packet to the pool after zeroing it.
func ReleasePacket(p *Packet) {
	if p == nil {
		return
	}
	p.Reset()
	packetPool.Put(p)
}

// Reset zeroes all fields so a pooled Packet can be safely reused.
func (p *Packet) Reset() {
	p.ID = 0
	p.Type = ""
	p.Body = nil
	p.PayloadSize = 0
	p.PayloadTransferInfo = nil
}

// NewPacket creates a new Packet with the current timestamp and given type/body.
func NewPacket(typ string, body interface{}) (*Packet, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("protocol: marshal body: %w", err)
	}
	return &Packet{
		ID:   time.Now().UnixMilli(),
		Type: typ,
		Body: raw,
	}, nil
}

// ReadPacket reads a single newline-delimited JSON packet from r.
// The returned Packet is from the pool — call ReleasePacket when done.
func ReadPacket(r *bufio.Reader) (*Packet, error) {
	// ReadSlice is zero-allocation: it points directly to the reader's internal buffer.
	line, err := r.ReadSlice('\n')
	if err != nil {
		if err == bufio.ErrBufferFull {
			// The packet is larger than the default 4KB buffer (e.g. embedded art).
			// Fall back to ReadBytes, which allocates a new slice to fit the rest.
			rest, errBytes := r.ReadBytes('\n')

			// Make a copy of line since ReadSlice data is volatile
			fullLine := make([]byte, len(line)+len(rest))
			copy(fullLine, line)
			copy(fullLine[len(line):], rest)
			line = fullLine
			err = errBytes
		} else if err == io.EOF {
			if len(line) == 0 {
				return nil, fmt.Errorf("protocol: read: %w", err)
			}
			if len(line) > 0 && line[len(line)-1] != '\n' {
				return nil, fmt.Errorf("protocol: read: truncated packet missing newline")
			}
		} else {
			return nil, fmt.Errorf("protocol: read: %w", err)
		}
	}

	if len(line) > MaxPacketSize {
		return nil, fmt.Errorf("protocol: packet too large (%d bytes, max %d)", len(line), MaxPacketSize)
	}

	if len(line) == 0 {
		return nil, fmt.Errorf("protocol: empty packet")
	}

	pkt := AcquirePacket()
	if err := json.Unmarshal(line, pkt); err != nil {
		ReleasePacket(pkt)
		return nil, fmt.Errorf("protocol: unmarshal: %w", err)
	}

	return pkt, nil
}

// WritePacket serializes a Packet as a single line of JSON followed by \n.
func WritePacket(w io.Writer, p *Packet) error {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("protocol: marshal: %w", err)
	}

	// Append newline — packets are line-delimited.
	data = append(data, '\n')

	_, err = w.Write(data)
	if err != nil {
		return fmt.Errorf("protocol: write: %w", err)
	}
	return nil
}
