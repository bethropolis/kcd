package sms

import (
	"context"
	"strings"
	"testing"

	"github.com/bethropolis/kcd/internal/config"
	"go.uber.org/zap"
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
	p := NewSMSPlugin(config.SMSConfig{}, nil, nil, zap.NewNop())
	for _, size := range []int64{0, -1, -999, maxSMSAttachmentBytes + 1} {
		err := p.receiveAttachment(context.Background(), nil, 0, size, "/nonexistent/x")
		if err == nil {
			t.Errorf("receiveAttachment(size=%d) = nil, want error", size)
		}
	}
}
