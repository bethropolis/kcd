package protocol

import "time"

// TypePair is the packet type for pairing requests/responses.
const TypePair = "kdeconnect.pair"

// Pairing direction constants.
const (
	PairAccept = true
	PairReject = false
)

// PairBody contains the fields of a pair packet body.
type PairBody struct {
	Pair      bool  `json:"pair"`
	Timestamp int64 `json:"timestamp,omitempty"`
}

// NewPairPacket creates a pairing packet (accept or reject).
func NewPairPacket(pair bool) (*Packet, error) {
	return NewPacket(TypePair, PairBody{
		Pair:      pair,
		Timestamp: time.Now().Unix(),
	})
}
