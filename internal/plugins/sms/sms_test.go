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
	if got := cleanFilename(strings.Repeat("a", 200) + ".jpg"); len(got) > maxFilenameLength+4 {
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
func (s *captureSender) SetName(string)                {}
func (s *captureSender) State() device.PairingState    { return device.StatePaired }
func (s *captureSender) SetState(device.PairingState)  {}
func (s *captureSender) Send(p *protocol.Packet) error { s.sent = append(s.sent, p); return nil }
func (s *captureSender) IsConnected() bool             { return true }
func (s *captureSender) RemoteIP() net.IP              { return nil }
func (s *captureSender) PeerCert() *x509.Certificate   { return nil }
func (s *captureSender) HasCapability(string) bool     { return true }
func (s *captureSender) UpdateBattery(int, bool)       {}
func (s *captureSender) GetBattery() (int, bool)       { return 0, false }

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
