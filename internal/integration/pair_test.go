//go:build integration

package integration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/testutil"
)

// nextDomainEvent returns the next non-bootstrap event on a watch stream,
// skipping the state.snapshot event the server sends first regardless of
// filters. Fails the test on timeout.
func nextDomainEvent(t *testing.T, evCh <-chan events.Event, timeout time.Duration) events.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-evCh:
			if ev.Type == events.TypeStateSnapshot {
				continue
			}
			return ev
		case <-deadline:
			t.Fatal("timed out waiting for event")
			return events.Event{}
		}
	}
}

func TestPairFlowIntegration(t *testing.T) {

	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := config.Defaults()
	cfg.SocketPath = dir + "/kcd.sock"
	cfg.CertFile = dir + "/cert.pem"
	cfg.KeyFile = dir + "/key.pem"
	cfg.DeviceID = "test-daemon-pair"
	cfg.LogLevel = "debug"
	// Minimal plugins
	cfg.Plugins.Battery = false
	cfg.Plugins.Notification = false
	cfg.Plugins.Clipboard = false
	cfg.Plugins.Share = false
	cfg.Plugins.RunCommand = false
	cfg.Plugins.MPRIS = false
	cfg.Plugins.Ping = false
	cfg.Plugins.Telephony = false
	cfg.Plugins.Connectivity = false
	cfg.Plugins.Mousepad = false
	cfg.Plugins.SFTP = false
	cfg.Plugins.FindMyPhone = false
	cfg.Plugins.LockDevice = false
	cfg.Plugins.SystemVolume = false
	cfg.Plugins.SMS = false

	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	_, cl := testutil.StartTestDaemon(t, cfg)

	// Subscribe to pair.accepted events
	evCh := make(chan events.Event, 4)
	go func() {
		_ = cl.Watch(context.Background(), []string{"pair.accepted"}, evCh)
	}()

	time.Sleep(100 * time.Millisecond)

	// Dial with mock peer
	peerCertFile := dir + "/peer-cert.pem"
	peerKeyFile := dir + "/peer-key.pem"
	peerCertPair, err := cert.LoadOrGenerate(peerCertFile, peerKeyFile, "mock-peer")
	if err != nil {
		t.Fatalf("peer cert: %v", err)
	}
	peerTlsCfg := cert.TLSConfig(peerCertPair)

	peer := testutil.NewMockPeer(t, peerTlsCfg)
	conn := peer.Dial("127.0.0.1:1716")
	defer conn.Close()

	// Read daemon identity
	_, _ = peer.ReadPacket(conn)

	// Send our identity back inside TLS
	identPkt, _ := protocol.NewPacket(protocol.TypeIdentity, protocol.IdentityBody{
		DeviceID:        "mock-peer",
		DeviceName:      "Mock Peer",
		DeviceType:      "phone",
		ProtocolVersion: protocol.ProtocolVersion,
	})
	_ = peer.SendPacket(conn, identPkt)

	// Send pair request
	pairPkt, err := protocol.NewPacket("kdeconnect.pair", map[string]bool{"pair": true})
	if err != nil {
		t.Fatalf("build pair pkt: %v", err)
	}
	if err := peer.SendPacket(conn, pairPkt); err != nil {
		t.Fatalf("send pair: %v", err)
	}

	// Give the daemon a moment to process the pair request
	time.Sleep(100 * time.Millisecond)

	// Accept the pending pair request via IPC (auto_accept removed in v1.10).
	// The peer initiated, so it owns the verification code and ours is empty.
	verificationKey, err := cl.Pair("mock-peer")
	if err != nil {
		t.Fatalf("accept pair: %v", err)
	}
	if verificationKey != "" {
		t.Errorf("accept path returned a verification key %q; the peer initiated", verificationKey)
	}

	ev := nextDomainEvent(t, evCh, 3*time.Second)
	if ev.Type != events.TypePairAccepted {
		t.Errorf("expected pair.accepted, got %s", ev.Type)
	}

	// Verify devices list shows mock-peer as Paired
	devs, err := cl.Devices()
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	found := false
	for _, d := range devs {
		if d.ID == "mock-peer" && d.State == device.StatePaired {
			found = true
		}
	}
	if !found {
		t.Errorf("mock-peer not found in Paired state; got: %+v", devs)
	}
}

// Initiating pairing must hand the verification code back to the caller:
// the phone shows its own copy, and the user can only compare codes if
// this side displays one. The accept path returns empty, so the code
// proves it came from the outbound request.
func TestPairInitiateReturnsVerificationKeyIntegration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := config.Defaults()
	cfg.SocketPath = dir + "/kcd.sock"
	cfg.CertFile = dir + "/cert.pem"
	cfg.KeyFile = dir + "/key.pem"
	cfg.DeviceID = "test-daemon-pair-init"
	cfg.LogLevel = "debug"
	cfg.Plugins.Battery = false
	cfg.Plugins.Notification = false
	cfg.Plugins.Clipboard = false
	cfg.Plugins.Share = false
	cfg.Plugins.RunCommand = false
	cfg.Plugins.MPRIS = false
	cfg.Plugins.Ping = false
	cfg.Plugins.Telephony = false
	cfg.Plugins.Connectivity = false
	cfg.Plugins.Mousepad = false
	cfg.Plugins.SFTP = false
	cfg.Plugins.FindMyPhone = false
	cfg.Plugins.LockDevice = false
	cfg.Plugins.SystemVolume = false
	cfg.Plugins.SMS = false

	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	_, cl := testutil.StartTestDaemon(t, cfg)

	peerCertPair, err := cert.LoadOrGenerate(dir+"/peer-cert.pem", dir+"/peer-key.pem", "mock-peer")
	if err != nil {
		t.Fatalf("peer cert: %v", err)
	}
	peer := testutil.NewMockPeer(t, cert.TLSConfig(peerCertPair))
	conn := peer.Dial("127.0.0.1:1716")
	defer conn.Close()

	_, _ = peer.ReadPacket(conn)

	// Identity only — no pair request. The daemon must initiate, so the
	// verification code is ours to report.
	identPkt, _ := protocol.NewPacket(protocol.TypeIdentity, protocol.IdentityBody{
		DeviceID:        "mock-peer",
		DeviceName:      "Mock Peer",
		DeviceType:      "phone",
		ProtocolVersion: protocol.ProtocolVersion,
	})
	if err := peer.SendPacket(conn, identPkt); err != nil {
		t.Fatalf("send identity: %v", err)
	}

	// Generous: under -race and a loaded CI runner the daemon's TLS
	// handshake and device registration can take several seconds.
	deadline := time.Now().Add(15 * time.Second)
	registered := false
	for time.Now().Before(deadline) {
		devs, err := cl.Devices()
		if err != nil {
			t.Fatalf("list devices: %v", err)
		}
		if len(devs) > 0 && devs[0].ID == "mock-peer" {
			registered = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !registered {
		t.Fatal("mock-peer never appeared in the device list")
	}

	verificationKey, err := cl.Pair("mock-peer")
	if err != nil {
		t.Fatalf("initiate pair: %v", err)
	}
	if len(verificationKey) != 8 {
		t.Fatalf("verification key %q: want 8 hex characters, got %d", verificationKey, len(verificationKey))
	}
	for _, r := range verificationKey {
		if !strings.ContainsRune("0123456789ABCDEF", r) {
			t.Fatalf("verification key %q contains a non-hex character %q", verificationKey, r)
		}
	}
}
