//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/testutil"
)

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
	cfg.Plugins.SendNotifications = false
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

	// Accept the pending pair request via IPC (auto_accept removed in v1.10)
	if err := cl.Pair("mock-peer"); err != nil {
		t.Fatalf("accept pair: %v", err)
	}

	select {
	case ev := <-evCh:
		if ev.Type != events.TypePairAccepted {
			t.Errorf("expected pair.accepted, got %s", ev.Type)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for pair.accepted event")
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
