//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/testutil"
)

// drainSnapshot skips the state.snapshot bootstrap event every watch stream
// starts with, returning the channel for domain events.
func drainSnapshot(t *testing.T, evCh <-chan events.Event) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-evCh:
			if ev.Type != events.TypeStateSnapshot {
				t.Fatalf("expected snapshot bootstrap first, got %s", ev.Type)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for snapshot bootstrap")
		}
	}
}

// nextFor returns the next event for deviceID, skipping LAN-neighbor noise.
// The test daemon binds :1716 on all interfaces, so real devices dial into
// it mid-run and their events share the watch stream.
func nextFor(t *testing.T, evCh <-chan events.Event, deviceID, what string) events.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-evCh:
			if ev.DeviceID != deviceID {
				continue
			}
			return ev
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
			return events.Event{}
		}
	}
}

// expectNoFor fails if an event for deviceID arrives within d; events for
// other (real-LAN) devices are ignored.
func expectNoFor(t *testing.T, evCh <-chan events.Event, deviceID string, d time.Duration, what string) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case ev := <-evCh:
			if ev.DeviceID == deviceID {
				t.Fatalf("expected no %s, got %s", what, ev.Type)
			}
		case <-deadline:
			return
		}
	}
}

// TestDuplicateSessionFailoverIntegration drives the roam race: a duplicate
// handshake inside the cooldown is refused pre-TLS (exactly one
// device.connected, zero device.disconnected); past the cooldown a second
// handshake replaces the live session (old socket closed, writer migrates),
// and the superseded session's death is silent.
func TestDuplicateSessionFailoverIntegration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := config.Defaults()
	cfg.SocketPath = dir + "/kcd.sock"
	cfg.CertFile = dir + "/cert.pem"
	cfg.KeyFile = dir + "/key.pem"
	cfg.DeviceID = "test-daemon-dedup"
	cfg.LogLevel = "debug"
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
	cfg.Plugins.SMS = false

	_, cl := testutil.StartTestDaemon(t, cfg)

	evCh := make(chan events.Event, 16)
	go func() {
		_ = cl.Watch(context.Background(),
			[]string{"device.connected", "device.disconnected", "battery.update"}, evCh)
	}()
	drainSnapshot(t, evCh)

	peerCertPair, err := cert.LoadOrGenerate(dir+"/peer-cert.pem", dir+"/peer-key.pem", "mock-peer")
	if err != nil {
		t.Fatalf("peer cert: %v", err)
	}
	peer := testutil.NewMockPeer(t, cert.TLSConfig(peerCertPair))

	// First session: full handshake + pair accept.
	connA := peer.Dial("127.0.0.1:1716")
	defer connA.Close()
	if _, err := peer.ReadPacket(connA); err != nil {
		t.Fatalf("read daemon identity: %v", err)
	}
	identPkt, _ := protocol.NewPacket(protocol.TypeIdentity, protocol.IdentityBody{
		DeviceID: "mock-peer", DeviceName: "Mock Peer",
		DeviceType: "phone", ProtocolVersion: protocol.ProtocolVersion,
	})
	if err := peer.SendPacket(connA, identPkt); err != nil {
		t.Fatalf("send identity: %v", err)
	}
	pairPkt, _ := protocol.NewPacket("kdeconnect.pair", map[string]bool{"pair": true})
	if err := peer.SendPacket(connA, pairPkt); err != nil {
		t.Fatalf("send pair: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := cl.Pair("mock-peer"); err != nil {
		t.Fatalf("accept pair: %v", err)
	}
	if ev := nextFor(t, evCh, "mock-peer", "device.connected"); ev.Type != events.TypeDeviceConnected {
		t.Fatalf("expected device.connected, got %s", ev.Type)
	}

	// Immediate duplicate handshake: refused inside the cooldown, no events.
	if _, err := peer.TryDial("127.0.0.1:1716"); err == nil {
		t.Fatal("expected duplicate handshake refused inside cooldown")
	}
	expectNoFor(t, evCh, "mock-peer", 500*time.Millisecond, "duplicate connect/disconnect")

	// Past the cooldown (1s, matching upstream Android) the same handshake
	// replaces the live session.
	time.Sleep(2 * time.Second)
	connC := peer.Dial("127.0.0.1:1716")
	defer connC.Close()
	if _, err := peer.ReadPacket(connC); err != nil {
		t.Fatalf("read daemon identity (C): %v", err)
	}
	if err := peer.SendPacket(connC, identPkt); err != nil {
		t.Fatalf("send identity (C): %v", err)
	}
	// Replacement is immediate; old socket is closed. The replacement
	// fires a fresh device.connected (same device, new session) while the
	// superseded session's death stays silent (no device.disconnected).
	if ev := nextFor(t, evCh, "mock-peer", "device.connected"); ev.Type != events.TypeDeviceConnected {
		t.Fatalf("expected device.connected on replacement, got %s", ev.Type)
	}
	expectNoFor(t, evCh, "mock-peer", 500*time.Millisecond, "replacement disconnect")
	// connA's peer should see EOF since it was replaced.
	_ = connA.SetReadDeadline(time.Now().Add(500 * time.Millisecond))

	// Survivor carries traffic on the new socket.
	battPkt, _ := protocol.NewPacket("kdeconnect.battery", map[string]any{
		"currentCharge": 55, "isCharging": false, "thresholdEvent": 0,
	})
	if err := peer.SendPacket(connC, battPkt); err != nil {
		t.Fatalf("send battery: %v", err)
	}
	if ev := nextFor(t, evCh, "mock-peer", "battery.update"); ev.Type != events.TypeBatteryUpdate {
		t.Fatalf("expected battery.update on survivor, got %s", ev.Type)
	}

	// Closing the superseded conn is silent; closing the current fires disconnect.
	if err := connA.Close(); err != nil {
		t.Fatalf("close A: %v", err)
	}
	expectNoFor(t, evCh, "mock-peer", 300*time.Millisecond, "superseded disconnect")
	if err := connC.Close(); err != nil {
		t.Fatalf("close C: %v", err)
	}
	if ev := nextFor(t, evCh, "mock-peer", "device.disconnected"); ev.Type != events.TypeDeviceDisconnected {
		t.Fatalf("expected device.disconnected, got %s", ev.Type)
	}
}
