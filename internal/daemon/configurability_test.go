package daemon

import (
	"context"
	"encoding/json"
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
	"net"
	"testing"
)

func TestReconnectConfiguredPort(t *testing.T) {
	dev := device.NewDevice("peer", "Peer", "phone", zap.NewNop())
	if got := reconnectPort(dev, 1816); got != 1816 {
		t.Fatalf("fallback = %d", got)
	}
	dev.SetLastPort(1916)
	if got := reconnectPort(dev, 1816); got != 1916 {
		t.Fatalf("peer port = %d", got)
	}
}

// A canceled dial cannot touch the network. The test documents acceptance of
// a non-default TCP port without changing the discovery protocol port.
func TestDialConfiguredPortCanceled(t *testing.T) {
	cfg := config.Defaults()
	cfg.TCPPort = 1816
	pkt, err := protocol.NewIdentityPacket("local", "Local", "desktop", cfg.TCPPort, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body protocol.IdentityBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.TCPPort != cfg.TCPPort {
		t.Fatalf("identity port = %d", body.TCPPort)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	logger := zap.NewNop()
	DialDevice(ctx, net.IPv4(127, 0, 0, 1), cfg.TCPPort, "peer", protocol.ProtocolVersion, pkt, nil, device.NewRegistry(nil), plugin.NewRegistry(logger), "local", logger, true, cfg)
}
