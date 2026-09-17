package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"go.uber.org/zap"
)

// startSidechannelServer accepts one TLS connection and streams N bytes to it.
// It reports the peer certificate so the test can pin the fingerprint.
func startSidechannelServer(t *testing.T, tlsConfig *tls.Config, payload []byte, delay time.Duration) (addr string, fp string, done <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	tlsCert := tlsConfig.Certificates[0]
	leaf, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	fp = cert.Fingerprint(leaf)

	doneCh := make(chan error, 1)
	done = doneCh
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			doneCh <- err
			return
		}
		defer conn.Close()
		tlsConn := tls.Server(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(context.Background()); err != nil {
			doneCh <- err
			return
		}
		// Simulate a slow sender: the payload takes longer than any sane
		// per-chunk idle deadline would allow, but is a healthy stream.
		chunk := 32 * 1024
		sent := 0
		for sent < len(payload) {
			n := chunk
			if sent+n > len(payload) {
				n = len(payload) - sent
			}
			if _, err := tlsConn.Write(payload[sent : sent+n]); err != nil {
				doneCh <- err
				return
			}
			sent += n
			if delay > 0 {
				time.Sleep(delay)
			}
		}
		doneCh <- nil
	}()
	return ln.Addr().String(), fp, done
}

// TestDialSidechannelRoundTrip verifies the helper streams a payload larger
// than the setup timeout window: the sidechannel timeout bounds connection
// establishment only, never payload I/O.
func TestDialSidechannelRoundTrip(t *testing.T) {
	payload := make([]byte, 256*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand: %v", err)
	}
	tlsCert, err := cert.GenerateSelfSigned("sidechannel_test_device")
	if err != nil {
		t.Fatalf("generate cert: %v", err)
	}
	tlsConfig := cert.TLSConfig(tlsCert)

	// A 256 KiB payload at 50 ms/chunk takes ~400 ms of streaming — long
	// after a setup timeout of 15s would have been exceeded if it applied
	// to reads. The transfer must still complete.
	addr, fp, serverDone := startSidechannelServer(t, tlsConfig, payload, 50*time.Millisecond)
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	conn, err := DialSidechannel(context.Background(), net.ParseIP(host), port, tlsConfig, fp, zap.NewNop(), SidechannelOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("DialSidechannel: %v", err)
	}
	defer conn.Close()

	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), len(payload))
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}
}

// TestDialSidechannelPinMismatch verifies the helper rejects a peer whose
// certificate does not match the pinned fingerprint, before any payload flows.
func TestDialSidechannelPinMismatch(t *testing.T) {
	tlsCert, err := cert.GenerateSelfSigned("sidechannel_evil_device")
	if err != nil {
		t.Fatalf("generate cert: %v", err)
	}
	tlsConfig := cert.TLSConfig(tlsCert)

	payload := []byte("secret")
	addr, _, _ := startSidechannelServer(t, tlsConfig, payload, 0)
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	wrongFP := "0000000000000000000000000000000000000000000000000000000000000000"
	_, err = DialSidechannel(context.Background(), net.ParseIP(host), port, tlsConfig, wrongFP, zap.NewNop(), SidechannelOptions{Timeout: 2 * time.Second})
	if err == nil {
		t.Fatal("expected pin verification failure, got nil error")
	}
	t.Logf("rejected as expected: %v", err)
}

// TestDialSidechannelSetupTimeout verifies the timeout bounds connection
// establishment: a peer that accepts TCP but never completes the TLS
// handshake must surface an error within roughly the configured budget.
func TestDialSidechannelSetupTimeout(t *testing.T) {
	// Plain TCP listener: accepts but never speaks TLS.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Hold the connection open without any TLS response.
		buf := make([]byte, 1024)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()

	tlsCert, err := cert.GenerateSelfSigned("sidechannel_timeout_device")
	if err != nil {
		t.Fatalf("generate cert: %v", err)
	}
	tlsConfig := cert.TLSConfig(tlsCert)

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	start := time.Now()
	_, err = DialSidechannel(context.Background(), net.ParseIP(host), port, tlsConfig, "", zap.NewNop(), SidechannelOptions{Timeout: 500 * time.Millisecond})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("setup timeout not enforced: took %v", elapsed)
	}
	t.Logf("timed out as expected after %v: %v", elapsed, err)
}

// TestDialSidechannelSurvivesContextCancel pins the establishment-only
// semantics: cancelling the caller's ctx after a successful dial must not
// abort an in-flight payload stream (Handle-scoped contexts die as soon as
// the packet handler returns, while download goroutines keep streaming).
func TestDialSidechannelSurvivesContextCancel(t *testing.T) {
	tlsCert, err := cert.GenerateSelfSigned("sidechannel_cancel_device")
	if err != nil {
		t.Fatalf("generate cert: %v", err)
	}
	tlsConfig := cert.TLSConfig(tlsCert)

	payload := make([]byte, 128*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand: %v", err)
	}
	addr, fp, serverDone := startSidechannelServer(t, tlsConfig, payload, 20*time.Millisecond)
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	ctx, cancel := context.WithCancel(context.Background())
	conn, err := DialSidechannel(ctx, net.ParseIP(host), port, tlsConfig, fp, zap.NewNop(), SidechannelOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("DialSidechannel: %v", err)
	}
	defer conn.Close()
	cancel() // die like a Handle-scoped context would

	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("stream must survive context cancellation: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), len(payload))
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}
}
