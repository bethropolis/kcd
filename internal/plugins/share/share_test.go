package share

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"go.uber.org/zap/zaptest"
)

func TestSharePlugin_SideChannelRoundTrip(t *testing.T) {
	logger := zaptest.NewLogger(t)
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.bin")
	destPath := filepath.Join(dir, "dest.bin")

	content := []byte("this is a test file for side-channel streaming!")
	if err := os.WriteFile(sourcePath, content, 0644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	// Generate TLS config for testing
	tlsCert, err := cert.GenerateSelfSigned("test_device_share")
	if err != nil {
		t.Fatalf("failed to generate cert: %v", err)
	}
	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{*tlsCert},
		InsecureSkipVerify: true,
		ClientAuth:         tls.RequireAnyClientCert,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Start Sender
	ln, port, err := ListenSideChannel(ctx, tlsConfig)
	if err != nil {
		t.Fatalf("ListenSideChannel failed: %v", err)
	}
	go func() {
		_ = AcceptAndSend(ln, sourcePath, tlsConfig, "test_device_share", nil, logger)
	}()

	// 2. Run Receiver (dial loopback)
	err = ReceiveSideChannel(ctx, net.ParseIP("127.0.0.1"), port, int64(len(content)), destPath, tlsConfig, nil, logger)
	if err != nil {
		t.Fatalf("ReceiveSideChannel failed: %v", err)
	}

	// 3. Verify
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read dest: %v", err)
	}

	if string(got) != string(content) {
		t.Errorf("content mismatch")
	}
}
