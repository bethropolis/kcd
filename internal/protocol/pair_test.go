package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewPairPacketTimestampRules(t *testing.T) {
	// Accept, reject and unpair packets carry no timestamp; only the
	// initial request does. Both sides derive the verification code from
	// the request's timestamp, so a fresh stamp on accept would split them.
	accept, err := NewPairPacket(PairAccept, 0)
	if err != nil {
		t.Fatalf("NewPairPacket accept: %v", err)
	}
	if strings.Contains(string(accept.Body), "timestamp") {
		t.Errorf("accept packet must omit timestamp, got %s", accept.Body)
	}

	const ts = int64(1711234567)
	req, err := NewPairPacket(PairAccept, ts)
	if err != nil {
		t.Fatalf("NewPairPacket request: %v", err)
	}
	var body PairBody
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if !body.Pair || body.Timestamp != ts {
		t.Errorf("request body = %+v, want pair:true timestamp:%d", body, ts)
	}
}
