// Package protocol implements the KDE Connect v8 packet framing.
// This package has zero external imports — stdlib only.
package protocol

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// MaxPacketSize is the maximum allowed line length for an authenticated packet.
// It matches Android's 32 MiB packet limit for large conversations and contacts.
const MaxPacketSize = 32 * 1024 * 1024

// MaxIdentityPacketSize limits unauthenticated identity packets. Identity data
// is small; a lower cap prevents a LAN peer from forcing large allocations
// before certificate authentication completes.
const MaxIdentityPacketSize = 512 * 1024

// ErrPacketTooLarge indicates that a complete line exceeded its framing limit.
// The reader has discarded the rest of that line and can continue safely.
var ErrPacketTooLarge = errors.New("protocol: packet too large")

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

// Pool diagnostics: acquires counts AcquirePacket calls, misses count fresh
// allocations when sync.Pool is empty (hits = acquires - misses).
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

// PoolStats returns packet pool usage counters for diagnostics; a miss rate
// above ~20% under steady load suggests GC-eviction pool pressure.
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

// NewPacket creates a Packet with the current timestamp. A nil body becomes
// {}: stock implementations always send objects, and null aborts phone links.
func NewPacket(typ string, body interface{}) (*Packet, error) {
	if body == nil {
		body = map[string]any{}
	}
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
	return ReadPacketBounded(r, MaxPacketSize)
}

// ReadPacketBounded reads one packet without retaining more than max bytes of
// an unterminated line. Oversized lines are discarded through their newline so
// the connection can continue with the next packet.
func ReadPacketBounded(r *bufio.Reader, max int) (*Packet, error) {
	line, err := readLineBounded(r, max)
	if err != nil {
		return nil, err
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

func readLineBounded(r *bufio.Reader, max int) ([]byte, error) {
	if max <= 0 {
		return nil, fmt.Errorf("protocol: invalid packet limit %d", max)
	}

	line := make([]byte, 0, min(max, 4096))
	for {
		chunk, err := r.ReadSlice('\n')
		if len(chunk) > max-len(line) {
			discardLine(r, chunk, err)
			return nil, fmt.Errorf("%w (%d bytes, max %d)", ErrPacketTooLarge, len(line)+len(chunk), max)
		}
		line = append(line, chunk...)

		if err == nil {
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			if len(line) == 0 {
				return nil, fmt.Errorf("protocol: read: %w", err)
			}
			return nil, fmt.Errorf("protocol: read: truncated packet missing newline")
		}
		return nil, fmt.Errorf("protocol: read: %w", err)
	}
}

func discardLine(r *bufio.Reader, chunk []byte, err error) {
	if err == nil || (len(chunk) > 0 && chunk[len(chunk)-1] == '\n') {
		return
	}
	for {
		chunk, err = r.ReadSlice('\n')
		if err == nil || (len(chunk) > 0 && chunk[len(chunk)-1] == '\n') || !errors.Is(err, bufio.ErrBufferFull) {
			return
		}
	}
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
