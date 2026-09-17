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

// NewPairPacket creates a pairing packet. Only the initial pair request
// carries a timestamp; accept, reject and unpair packets omit it. The
// verification code on both sides derives from the request's timestamp, so
// sending a fresh one on accept would desynchronize the displayed codes.
func NewPairPacket(pair bool, timestamp int64) (*Packet, error) {
	return NewPacket(TypePair, PairBody{
		Pair:      pair,
		Timestamp: timestamp,
	})
}
