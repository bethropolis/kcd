package protocol

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

// NewPairPacket creates a pairing packet. Only the initial request carries
// a timestamp: both codes derive from it, so a fresh one would desync them.
func NewPairPacket(pair bool, timestamp int64) (*Packet, error) {
	return NewPacket(TypePair, PairBody{
		Pair:      pair,
		Timestamp: timestamp,
	})
}
