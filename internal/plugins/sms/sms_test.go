package sms

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/fsutil"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

func TestCleanFilename(t *testing.T) {
	cases := []struct{ in, want string }{
		{"photo.jpg", "photo.jpg"},
		{"../../etc/passwd", "passwd"},
		{"/etc/passwd", "passwd"},
		{`..\..\evil.jpg`, "evil.jpg"},
		{`a\b\c.png`, "c.png"},
		{"", "downloaded_attachment"},
		{".", "downloaded_attachment"},
		{"..", "downloaded_attachment"},
		{"a\x00b.jpg", "ab.jpg"},
		{"a\nb.jpg", "ab.jpg"},
	}
	for _, tc := range cases {
		if got := cleanFilename(tc.in); got != tc.want {
			t.Errorf("cleanFilename(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := cleanFilename(strings.Repeat("a", 300) + ".jpg"); len(got) > fsutil.MaxFilenameBytes {
		t.Errorf("long filename not truncated: %d bytes", len(got))
	}
}

func TestReceiveAttachmentRejectsBadSize(t *testing.T) {
	p := NewSMSPlugin(config.SMSConfig{}, nil, nil, log.Nop())
	for _, size := range []int64{0, -1, -999, maxSMSAttachmentBytes + 1} {
		err := p.receiveAttachment(context.Background(), nil, 0, size, "/nonexistent/x", "")
		if err == nil {
			t.Errorf("receiveAttachment(size=%d) = nil, want error", size)
		}
	}
}

// captureSender implements device.Sender and records outbound packets.
type captureSender struct {
	sent []*protocol.Packet
}

func (s *captureSender) ID() string                    { return "test-device" }
func (s *captureSender) Name() string                  { return "test" }
func (s *captureSender) State() device.PairingState    { return device.StatePaired }
func (s *captureSender) Send(p *protocol.Packet) error { s.sent = append(s.sent, p); return nil }
func (s *captureSender) IsConnected() bool             { return true }
func (s *captureSender) RemoteIP() net.IP              { return nil }
func (s *captureSender) PeerCert() *x509.Certificate   { return nil }
func (s *captureSender) HasCapability(string) bool     { return true }

func TestSendSMSUsesV2Schema(t *testing.T) {
	p := NewSMSPlugin(config.SMSConfig{}, nil, nil, log.Nop())
	dev := &captureSender{}
	if err := p.SendSMS(dev, "+1234567890", "hello"); err != nil {
		t.Fatalf("SendSMS: %v", err)
	}
	if len(dev.sent) != 1 {
		t.Fatalf("sent %d packets, want 1", len(dev.sent))
	}
	var body struct {
		Version     int          `json:"version"`
		Addresses   []SMSAddress `json:"addresses"`
		MessageBody string       `json:"messageBody"`
	}
	if err := json.Unmarshal(dev.sent[0].Body, &body); err != nil {
		t.Fatalf("unmarshal send body: %v", err)
	}
	if body.Version != 2 {
		t.Errorf("version = %d, want 2", body.Version)
	}
	if len(body.Addresses) != 1 || body.Addresses[0].Address != "+1234567890" {
		t.Errorf("addresses = %+v, want recipient", body.Addresses)
	}
	if body.MessageBody != "hello" {
		t.Errorf("messageBody = %q, want %q", body.MessageBody, "hello")
	}
}

func TestMessagesBatchAcceptsIntReadFlag(t *testing.T) {
	// Android serializes the SQLite read column as a raw number (0/1).
	for _, read := range []string{"1", "0", "true", "false"} {
		pkt := &protocol.Packet{
			Type: PacketTypeSMSMessages,
			Body: json.RawMessage(`{"version":2,"messages":[{"event":1,"body":"hi","addresses":[{"address":"+1"}],"date":1711234567,"type":1,"thread_id":7,"read":` + read + `}]}`),
		}
		p := NewSMSPlugin(config.SMSConfig{}, nil, nil, log.Nop())
		if err := p.Handle(context.Background(), &captureSender{}, pkt); err != nil {
			t.Errorf("Handle with read=%s: %v, want nil", read, err)
		}
	}
}

func TestMessagesPublishAndroidMessageID(t *testing.T) {
	// Android sends the message id as _id (Telephony.Sms._ID), not u_id.
	bus := events.NewBus(log.Nop())
	sub := bus.Subscribe(1, events.TypeSMSIncoming)
	defer sub.Close()
	p := NewSMSPlugin(config.SMSConfig{}, bus, nil, log.Nop())
	pkt := &protocol.Packet{
		Type: PacketTypeSMSMessages,
		Body: json.RawMessage(`{"version":2,"messages":[{"event":1,"body":"hi","addresses":[{"address":"+1"}],"date":1711234567,"type":1,"thread_id":7,"read":1,"_id":4242}]}`),
	}
	if err := p.Handle(context.Background(), &captureSender{}, pkt); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	evt := <-sub.C
	if got := evt.Payload.(map[string]any)["u_id"]; got != int64(4242) {
		t.Fatalf("u_id = %v, want 4242", got)
	}
}

func TestSendSMSWithSubID(t *testing.T) {
	p := NewSMSPlugin(config.SMSConfig{}, nil, nil, log.Nop())
	dev := &captureSender{}
	if err := p.SendSMS(dev, "+1234567890", "hello", 2); err != nil {
		t.Fatalf("SendSMS: %v", err)
	}
	if len(dev.sent) != 1 {
		t.Fatalf("sent %d packets, want 1", len(dev.sent))
	}
	var body struct {
		SubID int64 `json:"subID"`
	}
	if err := json.Unmarshal(dev.sent[0].Body, &body); err != nil {
		t.Fatalf("unmarshal send body: %v", err)
	}
	if body.SubID != 2 {
		t.Errorf("subID = %d, want 2", body.SubID)
	}
}

func TestEmptyBodyWithAttachmentsKept(t *testing.T) {
	bus := events.NewBus(log.Nop())
	sub := bus.Subscribe(5, events.TypeSMSIncoming)
	defer sub.Close()
	p := NewSMSPlugin(config.SMSConfig{}, bus, nil, log.Nop())

	pkt := &protocol.Packet{
		Type: PacketTypeSMSMessages,
		Body: json.RawMessage(`{"version":2,"messages":[
			{"event":1,"body":"","addresses":[{"address":"+1"}],"date":1711234567,"type":1,"thread_id":7,"attachments":[{"part_id":1,"unique_identifier":"img1"}]},
			{"event":1,"body":"","addresses":[{"address":"+1"}],"date":1711234568,"type":1,"thread_id":7}
		]}`),
	}
	if err := p.Handle(context.Background(), &captureSender{}, pkt); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	select {
	case evt := <-sub.C:
		payload := evt.Payload.(map[string]any)
		if payload["thread_id"] != int64(7) {
			t.Errorf("thread_id = %v, want 7", payload["thread_id"])
		}
		if _, ok := payload["attachments"]; !ok {
			t.Errorf("attachments missing from payload")
		}
	default:
		t.Fatal("expected 1 incoming event for message with attachment")
	}

	// Verify the second message (empty body, no attachments) was dropped.
	select {
	case evt := <-sub.C:
		t.Fatalf("unexpected second event: %v", evt)
	default:
	}
}

func TestMessagesBatchTruncatedOverLimit(t *testing.T) {
	bus := events.NewBus(log.Nop())
	sub := bus.Subscribe(1100, events.TypeSMSIncoming)
	defer sub.Close()
	p := NewSMSPlugin(config.SMSConfig{}, bus, nil, log.Nop())

	// Build a batch with 1050 messages.
	messages := make([]map[string]any, 1050)
	for i := 0; i < 1050; i++ {
		messages[i] = map[string]any{
			"event":     1,
			"body":      "msg",
			"addresses": []map[string]string{{"address": "+1"}},
			"date":      1711234567 + int64(i),
			"type":      1,
			"thread_id": 7,
			"read":      1,
			"_id":       i,
		}
	}
	rawBatch, _ := json.Marshal(map[string]any{
		"version":  2,
		"messages": messages,
	})
	pkt := &protocol.Packet{
		Type: PacketTypeSMSMessages,
		Body: rawBatch,
	}

	if err := p.Handle(context.Background(), &captureSender{}, pkt); err != nil {
		t.Fatalf("Handle failed on large batch: %v", err)
	}

	// Should have published exactly maxSMSMessages (1000).
	count := 0
	for {
		select {
		case <-sub.C:
			count++
		default:
			goto done
		}
	}
done:
	if count != maxSMSMessages {
		t.Errorf("published %d messages, want %d", count, maxSMSMessages)
	}
}

func TestRequestConversationDefaultLimit(t *testing.T) {
	p := NewSMSPlugin(config.SMSConfig{}, nil, nil, log.Nop())
	dev := &captureSender{}

	if err := p.RequestConversation(dev, 42, -1, -1); err != nil {
		t.Fatalf("RequestConversation: %v", err)
	}
	if len(dev.sent) != 1 {
		t.Fatalf("sent %d packets, want 1", len(dev.sent))
	}
	var body map[string]any
	if err := json.Unmarshal(dev.sent[0].Body, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["threadID"] != float64(42) {
		t.Errorf("threadID = %v, want 42", body["threadID"])
	}
	if body["numberToRequest"] != float64(500) {
		t.Errorf("numberToRequest = %v, want 500", body["numberToRequest"])
	}
}

func TestAttachmentThreadIDTracking(t *testing.T) {
	bus := events.NewBus(log.Nop())
	p := NewSMSPlugin(config.SMSConfig{}, bus, nil, log.Nop())
	dev := &captureSender{}

	// Request attachment with threadID 99
	if err := p.RequestAttachment(dev, 123, "att-uuid-xyz", 99); err != nil {
		t.Fatalf("RequestAttachment: %v", err)
	}

	// Verify the uniqueIdentifier was mapped to threadID 99.
	v, ok := p.attachmentThreads.Load("att-uuid-xyz")
	if !ok || v.(int64) != 99 {
		t.Errorf("attachmentThreads not tracked correctly: %v", v)
	}
}
