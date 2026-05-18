//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/testutil"
)

func TestBatteryUpdateFlowIntegration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := config.Defaults()
	cfg.SocketPath = dir + "/kcd.sock"
	cfg.CertFile = dir + "/cert.pem"
	cfg.KeyFile = dir + "/key.pem"
	cfg.DeviceID = "test-daemon-battery"
	cfg.LogLevel = "debug"
	// Minimal plugins
	cfg.Plugins.Battery = true
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

	// Pre-generate cert so daemon doesn't need to write to disk
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	_, cl := testutil.StartTestDaemon(t, cfg)

	// Subscribe to battery events
	evCh := make(chan events.Event, 4)
	go func() {
		_ = cl.Watch(context.Background(), []string{"battery.update"}, evCh)
	}()

	// Give watch a moment to connect
	time.Sleep(100 * time.Millisecond)

	// Dial daemon TCP port with a mock peer
	peerCertFile := dir + "/peer-cert.pem"
	peerKeyFile := dir + "/peer-key.pem"
	peerCertPair, err := cert.LoadOrGenerate(peerCertFile, peerKeyFile, "mock-peer")
	if err != nil {
		t.Fatalf("peer cert: %v", err)
	}
	peerTlsCfg := cert.TLSConfig(peerCertPair)

	peer := testutil.NewMockPeer(t, peerTlsCfg)
	conn := peer.Dial(net.JoinHostPort("127.0.0.1", "1716"))
	defer conn.Close()

	// Read the daemon's identity reply
	_, _ = peer.ReadPacket(conn)

	// Send our identity back inside TLS
	identPkt, _ := protocol.NewPacket(protocol.TypeIdentity, protocol.IdentityBody{
		DeviceID:        "mock-peer",
		DeviceName:      "Mock Peer",
		DeviceType:      "phone",
		ProtocolVersion: protocol.ProtocolVersion,
	})
	_ = peer.SendPacket(conn, identPkt)

	// Send pair request to get into Paired state
	pairPkt, _ := protocol.NewPacket("kdeconnect.pair", map[string]bool{"pair": true})
	_ = peer.SendPacket(conn, pairPkt)

	// Give daemon time to process the pair request
	time.Sleep(100 * time.Millisecond)

	// Accept the pending pair request via IPC (auto_accept removed in v1.10)
	if err := cl.Pair("mock-peer"); err != nil {
		t.Fatalf("accept pair: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Send battery packet
	battPkt, err := protocol.NewPacket("kdeconnect.battery", map[string]interface{}{
		"currentCharge":  77,
		"isCharging":     true,
		"thresholdEvent": 0,
	})
	if err != nil {
		t.Fatalf("build battery pkt: %v", err)
	}
	if err := peer.SendPacket(conn, battPkt); err != nil {
		t.Fatalf("send battery: %v", err)
	}

	select {
	case ev := <-evCh:
		payload, _ := json.Marshal(ev.Payload)
		t.Logf("battery event: %s", payload)
		if ev.Type != events.TypeBatteryUpdate {
			t.Errorf("expected battery.update, got %s", ev.Type)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for battery.update event")
	}
}
