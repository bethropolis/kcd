package discovery

import (
	"context"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

func testIdentity(t *testing.T) *protocol.Packet {
	t.Helper()
	pkt, err := protocol.NewIdentityPacket("test-id", "Test", "desktop", 1716, nil, nil)
	if err != nil {
		t.Fatalf("NewIdentityPacket failed: %v", err)
	}
	return pkt
}

func TestConfiguredBroadcastIntervals(t *testing.T) {
	bc := NewBroadcasterController(testIdentity(t), 7*time.Second, zap.NewNop(), nil, 19*time.Second)
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
	logger := zap.NewNop()
	bc := NewBroadcasterController(testIdentity(t), time.Hour, logger, nil)
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
