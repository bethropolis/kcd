package transport

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/log"
)

func TestReceiveToFileAtomicAndBounded(t *testing.T) {
	serverCert, err := cert.GenerateSelfSigned("receiver")
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg := cert.TLSConfig(serverCert)
	addr, fp, done := startSidechannelServer(t, tlsCfg, []byte("new-content"), 0)
	host, portText, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portText)
	dest := filepath.Join(t.TempDir(), "result")
	if err := os.WriteFile(dest, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = ReceiveToFile(context.Background(), net.ParseIP(host), port, 20, dest, tlsCfg, fp, log.Nop(), ReceiveOptions{MaxSize: 64, Sidechannel: SidechannelOptions{Timeout: 2 * time.Second}})
	if err == nil {
		t.Fatal("expected short-read failure")
	}
	got, readErr := os.ReadFile(dest)
	if readErr != nil || string(got) != "original" {
		t.Fatalf("destination changed on failed transfer: %q, %v", got, readErr)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestReceiveToFileRejectsOversizeBeforeDial(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "result")
	_, err := ReceiveToFile(context.Background(), net.ParseIP("127.0.0.1"), 1, 11, dest, nil, "", log.Nop(), ReceiveOptions{MaxSize: 10})
	if err == nil {
		t.Fatal("expected declared size rejection")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("destination unexpectedly created: %v", statErr)
	}
}

func TestReceiveToFileAcceptsEmptyPayload(t *testing.T) {
	serverCert, err := cert.GenerateSelfSigned("empty-receiver")
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg := cert.TLSConfig(serverCert)
	addr, fp, done := startSidechannelServer(t, tlsCfg, nil, 0)
	host, portText, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portText)
	dest := filepath.Join(t.TempDir(), "empty")
	n, err := ReceiveToFile(context.Background(), net.ParseIP(host), port, 0, dest, tlsCfg, fp, log.Nop(), ReceiveOptions{MaxSize: 10, Sidechannel: SidechannelOptions{Timeout: 2 * time.Second}})
	if err != nil || n != 0 {
		t.Fatalf("receive = %d, %v", n, err)
	}
	info, err := os.Stat(dest)
	if err != nil || info.Size() != 0 {
		t.Fatalf("empty destination: %v, %v", info, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestReceiveToFileStreamsUnknownLengthToEOF(t *testing.T) {
	serverCert, err := cert.GenerateSelfSigned("unknown-length-receiver")
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg := cert.TLSConfig(serverCert)
	want := []byte("cloud-backed payload")
	addr, fp, done := startSidechannelServer(t, tlsCfg, want, 0)
	host, portText, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portText)
	dest := filepath.Join(t.TempDir(), "payload")
	n, err := ReceiveToFile(context.Background(), net.ParseIP(host), port, -1, dest, tlsCfg, fp, log.Nop(), ReceiveOptions{MaxSize: 128, Sidechannel: SidechannelOptions{Timeout: 2 * time.Second}})
	if err != nil || n != int64(len(want)) {
		t.Fatalf("receive = %d, %v", n, err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("payload = %q, %v; want %q", got, err, want)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
