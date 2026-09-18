package contacts

import (
	"context"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
)

// Handle routes response packets; all parsing and disk I/O runs in a
// worker goroutine so Handle returns immediately (rule 9).
func (p *ContactsPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	switch pkt.Type {
	case PacketTypeContactsResponseUIDs:
		body := append([]byte(nil), pkt.Body...)
		go p.handleUIDsResponse(dev, body)
		return nil
	case PacketTypeContactsResponseVCards:
		body := append([]byte(nil), pkt.Body...)
		devID := dev.ID()
		go p.handleVCardsResponse(devID, body)
		return nil
	default:
		return nil
	}
}

// RequestSync asks the phone for all contact UIDs and timestamps, which
// starts the sync round trips. Responses arrive async via Handle.
//
// The body must be an empty object, not null: stock implementations send
// `"body":{}` for bodyless requests, and at least one phone build aborts
// the whole link on an explicit null body (observed as an immediate RST
// after every connect that carried `"body":null`).
func (p *ContactsPlugin) RequestSync(dev device.Sender) error {
	pkt, err := protocol.NewPacket(PacketTypeContactsRequestUIDs, map[string]any{})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

// requestVCards asks for vCards of the given UIDs, chunked to bound
// outbound packet size.
func (p *ContactsPlugin) requestVCards(dev device.Sender, uids []string) error {
	for _, chunk := range chunkUIDs(uids, vcardChunkSize) {
		body := map[string]any{"uids": chunk}
		pkt, err := protocol.NewPacket(PacketTypeContactsRequestVCards, body)
		if err != nil {
			return err
		}
		if err := dev.Send(pkt); err != nil {
			return err
		}
	}
	return nil
}

func chunkUIDs(uids []string, size int) [][]string {
	var chunks [][]string
	for len(uids) > 0 {
		n := size
		if len(uids) < n {
			n = len(uids)
		}
		chunks = append(chunks, uids[:n])
		uids = uids[n:]
	}
	return chunks
}

func (p *ContactsPlugin) emit(deviceID string, payload map[string]any) {
	if p.bus == nil {
		return
	}
	p.bus.Publish(events.TypeContactsUpdated, deviceID, payload)
}
