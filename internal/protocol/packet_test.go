package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestReadPacket_Valid(t *testing.T) {
	input := `{"id":123,"type":"test","body":{"foo":"bar"}}` + "\n"
	r := bufio.NewReader(strings.NewReader(input))

	pkt, err := ReadPacket(r)
	if err != nil {
		t.Fatalf("ReadPacket failed: %v", err)
	}
	defer ReleasePacket(pkt)

	if pkt.ID != 123 {
		t.Errorf("expected ID 123, got %d", pkt.ID)
	}
	if pkt.Type != "test" {
		t.Errorf("expected Type 'test', got %q", pkt.Type)
	}
	if string(pkt.Body) != `{"foo":"bar"}` {
		t.Errorf("expected Body `{\"foo\":\"bar\"}`, got %q", pkt.Body)
	}
}

func TestReadPacket_Truncated(t *testing.T) {
	// Missing newline
	input := `{"id":123,"type":"test","body":{"foo":"bar"}}`
	r := bufio.NewReader(strings.NewReader(input))

	_, err := ReadPacket(r)
	if err == nil {
		t.Fatal("expected error for missing newline, got nil")
	}
}

func TestReadPacket_Oversized(t *testing.T) {
	// Create a line just larger than MaxPacketSize
	input := `{"id":1, "type":"big", "body":"` + strings.Repeat("A", MaxPacketSize) + `"}` + "\n"
	r := bufio.NewReader(strings.NewReader(input))

	_, err := ReadPacket(r)
	if err == nil {
		t.Fatal("expected error for oversized packet, got nil")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected 'too large' error, got: %v", err)
	}
}

func TestWritePacket_Valid(t *testing.T) {
	pkt := &Packet{
		ID:   456,
		Type: "test_write",
		Body: []byte(`{"a":1}`),
	}

	var buf bytes.Buffer
	err := WritePacket(&buf, pkt)
	if err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}

	output := buf.String()
	if !strings.HasSuffix(output, "\n") {
		t.Error("WritePacket output is missing trailing newline")
	}

	expected := `{"id":456,"type":"test_write","body":{"a":1}}` + "\n"
	if output != expected {
		t.Errorf("expected %q, got %q", expected, output)
	}
}

func TestIdentityPacketVersion(t *testing.T) {
	pkt, err := NewIdentityPacket("id1", "name1", "desktop", 1716, nil, nil)
	if err != nil {
		t.Fatalf("NewIdentityPacket failed: %v", err)
	}

	var body IdentityBody

	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		t.Fatalf("Unmarshal body failed: %v", err)
	}

	if body.ProtocolVersion != 8 {
		t.Errorf("expected ProtocolVersion 8, got %d", body.ProtocolVersion)
	}
}

func TestReadPacket_LargeContactsSync(t *testing.T) {
	// Regression test for contacts sync killing the link with
	// "protocol: unmarshal: invalid character 'f' after top-level value".
	// Packets larger than bufio's default 4096-byte buffer trigger
	// bufio.ErrBufferFull in ReadPacket; the first chunk must be copied
	// before ReadBytes refills the reader's internal buffer.
	const uidCount = 1000
	uids := make([]string, 0, uidCount)
	bodyMap := make(map[string]any, uidCount+1)
	for i := 0; i < uidCount; i++ {
		// Deterministic unique UIDs with 'f' runs — the corruption
		// signature was "invalid character 'f' after top-level value".
		uid := fmt.Sprintf("contact-uid-ffff-%04d-ffffffff", i)
		uids = append(uids, uid)
		bodyMap[uid] = "1730000000"
	}
	bodyMap["uids"] = uids

	pktIn, err := NewPacket("kdeconnect.contacts.response_uids_timestamps", bodyMap)
	if err != nil {
		t.Fatalf("NewPacket failed: %v", err)
	}
	pktIn.ID = 424242

	var buf bytes.Buffer
	if err := WritePacket(&buf, pktIn); err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}
	if buf.Len() <= 8192 {
		t.Fatalf("test packet too small to exercise ErrBufferFull path: %d bytes", buf.Len())
	}

	// Default bufio buffer is 4096 bytes, so this forces ErrBufferFull.
	r := bufio.NewReader(bytes.NewReader(buf.Bytes()))
	pktOut, err := ReadPacket(r)
	if err != nil {
		t.Fatalf("ReadPacket failed on large contacts packet (%d bytes): %v", buf.Len(), err)
	}
	defer ReleasePacket(pktOut)

	if pktOut.Type != "kdeconnect.contacts.response_uids_timestamps" {
		t.Errorf("expected contacts type, got %q", pktOut.Type)
	}
	if pktOut.ID != 424242 {
		t.Errorf("expected ID 424242, got %d", pktOut.ID)
	}
	var body struct {
		UIDs []string `json:"uids"`
	}
	if err := json.Unmarshal(pktOut.Body, &body); err != nil {
		t.Fatalf("body unmarshal failed (corrupted?): %v", err)
	}
	if len(body.UIDs) != uidCount {
		t.Fatalf("expected %d uids, got %d", uidCount, len(body.UIDs))
	}
	if body.UIDs[0] != uids[0] || body.UIDs[uidCount-1] != uids[uidCount-1] {
		t.Error("uid content mismatch — first chunk likely clobbered")
	}
}

func TestPacketPool(t *testing.T) {
	pkt := AcquirePacket()
	pkt.ID = 999
	pkt.Type = "pool_test"
	ReleasePacket(pkt)

	pkt2 := AcquirePacket()
	if pkt2.ID != 0 || pkt2.Type != "" {
		t.Errorf("pool did not reset packet fields: got ID=%d Type=%q", pkt2.ID, pkt2.Type)
	}
	ReleasePacket(pkt2)
}
