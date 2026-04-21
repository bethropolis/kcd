package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
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
