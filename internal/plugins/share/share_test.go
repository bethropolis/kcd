package share

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/log"
)

func TestSharePlugin_SideChannelRoundTrip(t *testing.T) {
	logger := log.NewTest(t)
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

	leaf, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse test cert: %v", err)
	}
	fp := cert.Fingerprint(leaf)

	// 1. Start Sender
	cfg := config.ShareConfig{}
	cfg.Defaults()
	ln, port, err := ListenSideChannel(ctx, cfg, tlsConfig)
	if err != nil {
		t.Fatalf("ListenSideChannel failed: %v", err)
	}
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- AcceptAndSend(ln, sourcePath, tlsConfig, "test_device_share", fp, 2*time.Second, func(c, t int64) {}, logger)
	}()

	// 2. Run Receiver (dial loopback)
	err = ReceiveSideChannel(ctx, net.ParseIP("127.0.0.1"), port, int64(len(content)), destPath, tlsConfig, fp, nil, logger)
	if err != nil {
		t.Fatalf("ReceiveSideChannel failed: %v", err)
	}

	// Join the server goroutine before returning: it holds the zaptest
	// logger, which panics on use after the test completes.
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("AcceptAndSend failed: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for side-channel server to finish")
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

func TestSharePlugin_SideChannelRejectsWrongFingerprint(t *testing.T) {
	logger := log.NewTest(t)
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.bin")
	destPath := filepath.Join(dir, "dest.bin")

	content := []byte("fingerprint mismatch test")
	if err := os.WriteFile(sourcePath, content, 0644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	tlsCert, err := cert.GenerateSelfSigned("test_device_share")
	if err != nil {
		t.Fatalf("failed to generate cert: %v", err)
	}
	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{*tlsCert},
		InsecureSkipVerify: true,
		ClientAuth:         tls.RequireAnyClientCert,
	}
	leaf, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse test cert: %v", err)
	}
	fp := cert.Fingerprint(leaf)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := config.ShareConfig{}
	cfg.Defaults()
	// Isolate from the round-trip test's ports: both bind from PortMin
	// upward, and a lingering listener would otherwise collide.
	cfg.PortMin = 1762
	cfg.PortMax = 1764
	ln, port, err := ListenSideChannel(ctx, cfg, tlsConfig)
	if err != nil {
		t.Fatalf("ListenSideChannel failed: %v", err)
	}
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- AcceptAndSend(ln, sourcePath, tlsConfig, "test_device_share", fp, 2*time.Second, nil, logger)
	}()

	// Dial with a wrong expected fingerprint: the receiver must refuse
	// before writing anything.
	err = ReceiveSideChannel(ctx, net.ParseIP("127.0.0.1"), port, int64(len(content)), destPath, tlsConfig, strings.Repeat("0", 64), nil, logger)
	if err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("expected fingerprint verification failure, got: %v", err)
	}

	// Join the server goroutine before returning: it holds the zaptest
	// logger, which panics on use after the test completes. The server
	// side is expected to fail streaming to the aborted client — any
	// outcome is fine; the assertion above is on the receiver side.
	select {
	case <-serverDone:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for side-channel server to finish")
	}
}
