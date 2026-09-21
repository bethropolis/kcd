package discovery

import (
	"context"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

func testIdentity(t *testing.T) *protocol.Packet {
	t.Helper()
	pkt, err := protocol.NewIdentityPacket("test-id", "Test", "desktop", protocol.DefaultTCPPort, nil, nil)
	if err != nil {
		t.Fatalf("NewIdentityPacket failed: %v", err)
	}
	return pkt
}

func TestConfiguredBroadcastIntervals(t *testing.T) {
	bc := NewBroadcasterController(testIdentity(t), protocol.DefaultTCPPort, 7*time.Second, log.Nop(), nil, 19*time.Second)
	if bc.interval != 7*time.Second || bc.idleInterval != 19*time.Second {
		t.Fatal("configured intervals not stored")
	}
	if bc.IsRunning() {
		t.Fatal("configuration must not start broadcasts")
	}
}

// Pairing and reconnect needs share one loop but must not cancel each
// other: withdrawing one owner leaves the loop up while the other holds it,
// and the loop stops only when the last owner withdraws.
func TestBroadcasterOwners(t *testing.T) {
	logger := log.Nop()
	bc := NewBroadcasterController(testIdentity(t), protocol.DefaultTCPPort, time.Hour, logger, nil)
	ctx := context.Background()

	if bc.IsRunning() {
		t.Fatal("controller must start stopped")
	}

	bc.Start(ctx) // pairing owner
	if !bc.IsRunning() {
		t.Fatal("Start must run the loop")
	}
	bc.StopOwned(OwnerReconnect) // never held: no-op
	if !bc.IsRunning() {
		t.Error("withdrawing a non-owner must not stop the loop")
	}

	bc.StartOwned(ctx, OwnerReconnect)
	bc.Stop() // pairing withdraws; reconnect still holds
	if !bc.IsRunning() {
		t.Error("loop must survive while reconnect owner holds it")
	}

	bc.StopOwned(OwnerReconnect) // last owner out
	if bc.IsRunning() {
		t.Error("loop must stop when no owners remain")
	}
}

// A non-default tcp_port must reach the broadcaster: the controller stores
// it and hands it to every Broadcaster it spawns.
func TestBroadcasterControllerStoresPort(t *testing.T) {
	bc := NewBroadcasterController(testIdentity(t), 1816, time.Hour, log.Nop(), nil)
	if bc.port != 1816 {
		t.Fatalf("controller port = %d, want 1816", bc.port)
	}
	b := NewBroadcaster(testIdentity(t), 1816, time.Hour, log.Nop())
	if b.port != 1816 {
		t.Fatalf("broadcaster port = %d, want 1816", b.port)
	}
}
