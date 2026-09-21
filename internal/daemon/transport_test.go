package daemon

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/discovery"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
)

func ephemeralDevice(state device.PairingState, markEphemeral, pairIntent bool) *device.Device {
	dev := device.NewDevice("test-id", "Test", "phone", log.Nop())
	dev.SetState(state)
	if markEphemeral {
		dev.MarkEphemeralDialed()
	}
	if pairIntent {
		dev.RequestPairDial()
	}
	return dev
}

func TestShouldEphemeralClose(t *testing.T) {
	cases := []struct {
		name        string
		dev         *device.Device
		pairingMode bool
		want        bool
	}{
		{"pairing mode keeps", ephemeralDevice(device.StateUnpaired, true, false), true, false},
		{"never dialled keeps", ephemeralDevice(device.StateUnpaired, false, false), false, false},
		{"paired keeps", ephemeralDevice(device.StatePaired, true, false), false, false},
		{"pair requested keeps", ephemeralDevice(device.StatePairRequested, true, false), false, false},
		{"pair requested by peer keeps", ephemeralDevice(device.StatePairRequestedByPeer, true, false), false, false},
		{"explicit intent keeps", ephemeralDevice(device.StateUnpaired, true, true), false, false},
		{"idle stranger closes", ephemeralDevice(device.StateUnpaired, true, false), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldEphemeralClose(tc.dev, tc.pairingMode); got != tc.want {
				t.Errorf("shouldEphemeralClose() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEphemeralMarkerReset(t *testing.T) {
	dev := device.NewDevice("test-id", "Test", "phone", log.Nop())
	if dev.EphemeralDialed() {
		t.Fatal("new device must not be ephemeral-marked")
	}
	dev.MarkEphemeralDialed()
	if !dev.EphemeralDialed() {
		t.Fatal("marker not set")
	}
	dev.ClearEphemeral()
	if dev.EphemeralDialed() {
		t.Fatal("marker not cleared")
	}
	if dev.PairDialPending() {
		t.Fatal("new device must have no pair intent")
	}
	dev.RequestPairDial()
	if !dev.PairDialPending() {
		t.Fatal("pair intent not visible")
	}
	if !dev.ConsumePairDial() {
		t.Fatal("pair intent not consumed")
	}
	if dev.PairDialPending() {
		t.Fatal("pair intent not cleared after consume")
	}
}

func TestPairDialIntentLifetime(t *testing.T) {
	dev := device.NewDevice("test-id", "Test", "phone", log.Nop())
	dev.MarkEphemeralDialed()

	if dev.PairDialActive() {
		t.Fatal("new device must have no active pair intent")
	}

	dev.RequestPairDial()
	if !dev.PairDialActive() {
		t.Fatal("pair intent not active after request")
	}

	// Consuming the one-shot dial trigger must NOT clear the keep-alive
	// intent: a slow phone-side accept must not downgrade into an
	// ephemeral-close flap.
	if !dev.ConsumePairDial() {
		t.Fatal("pair intent not consumed")
	}
	if !dev.PairDialActive() {
		t.Fatal("keep-alive intent lost after one-shot consume")
	}
	if shouldEphemeralClose(dev, false) {
		t.Fatal("explicit intent must pin the connection after consume")
	}

	dev.ClearPairDial()
	if dev.PairDialActive() {
		t.Fatal("pair intent not cleared")
	}
	if !shouldEphemeralClose(dev, false) {
		t.Fatal("idle stranger should close once intent is cleared")
	}
}

func TestLastPortRoundTrip(t *testing.T) {
	dev := device.NewDevice("test-id", "Test", "phone", log.Nop())
	if dev.LastPort() != 0 {
		t.Fatal("new device must have unknown port")
	}
	dev.SetLastPort(1716)
	if dev.LastPort() != 1716 {
		t.Fatalf("LastPort() = %d, want 1716", dev.LastPort())
	}
}

func TestShouldDiscoveryDialThrottle(t *testing.T) {
	dev := device.NewDevice("test-id", "Test", "phone", log.Nop())
	if !dev.ShouldDiscoveryDial(10 * time.Second) {
		t.Fatal("first dial must be allowed")
	}
	if dev.ShouldDiscoveryDial(10 * time.Second) {
		t.Fatal("immediate redial must be throttled")
	}
	if !dev.ShouldDiscoveryDial(0) {
		t.Fatal("zero interval must always allow")
	}
}

func newTestIdentity() (*protocol.Packet, error) {
	return protocol.NewIdentityPacket("test-id", "Test", "desktop", protocol.DefaultTCPPort, nil, nil)
}

func TestSyncReconnectBroadcast(t *testing.T) {
	logger := log.Nop()
	ctx := context.Background()
	identity, err := newTestIdentity()
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	newBC := func() *discovery.BroadcasterController {
		return discovery.NewBroadcasterController(identity, protocol.DefaultTCPPort, time.Hour, logger, nil)
	}

	// Offline paired device starts the reconnect broadcast.
	reg := device.NewRegistry(nil)
	offline := device.NewDevice("off", "Phone", "phone", logger)
	offline.SetState(device.StatePaired)
	reg.Add(offline)
	bc := newBC()
	syncReconnectBroadcast(ctx, reg, bc)
	if !bc.IsRunning() {
		t.Error("offline paired device must start reconnect broadcast")
	}

	// Unpaired strangers never trigger it.
	reg2 := device.NewRegistry(nil)
	stranger := device.NewDevice("str", "Stranger", "phone", logger)
	reg2.Add(stranger)
	bc2 := newBC()
	syncReconnectBroadcast(ctx, reg2, bc2)
	if bc2.IsRunning() {
		t.Error("unpaired devices must not start reconnect broadcast")
		bc2.StopOwned(discovery.OwnerReconnect)
	}

	// Clearing the registry withdraws the owner again.
	reg.Remove("off")
	syncReconnectBroadcast(ctx, reg, bc)
	if bc.IsRunning() {
		t.Error("no offline pairs must stop reconnect broadcast")
		bc.StopOwned(discovery.OwnerReconnect)
	}

	// Pairing-owned broadcast survives the sync withdrawing reconnect.
	bc3 := newBC()
	bc3.Start(ctx) // pairing owner
	syncReconnectBroadcast(ctx, reg2, bc3)
	if !bc3.IsRunning() {
		t.Error("sync must not stop pairing-owned broadcast")
	}
	bc3.Stop()
	if bc3.IsRunning() {
		t.Error("pairing stop must end an unowned loop")
	}
}

// A manual connect-by-IP dial must not address the pre-TLS identity to any
// device: stock peers close identities carrying a foreign targetDeviceId,
// so the field has to stay absent. Dials with a known target keep it.
func TestDialPreTLSIdentityTarget(t *testing.T) {
	cases := []struct {
		name       string
		targetID   string
		wantTarget string // empty means the key must be absent
	}{
		{"manual dial omits target", "", ""},
		{"known target kept", "peer-device-id", "peer-device-id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			got := make(chan []byte, 1)
			go func() {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				pkt, _, err := transport.ReadPlaintextPacket(c)
				if err != nil {
					return
				}
				defer protocol.ReleasePacket(pkt)
				got <- append([]byte(nil), pkt.Body...)
			}()
			cfg := config.Defaults()
			pkt, err := protocol.NewIdentityPacket("local", "Local", "desktop", cfg.TCPPort, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			logger := log.Nop()
			addr := ln.Addr().(*net.TCPAddr)
			DialDevice(context.Background(), addr.IP, addr.Port, tc.targetID, protocol.ProtocolVersion, pkt, &tls.Config{}, device.NewRegistry(nil), plugin.NewRegistry(logger), "local", logger, true, cfg)
			select {
			case body := <-got:
				var fields map[string]any
				if err := json.Unmarshal(body, &fields); err != nil {
					t.Fatalf("unmarshal pre-TLS body: %v", err)
				}
				v, present := fields["targetDeviceId"]
				if tc.wantTarget == "" {
					if present {
						t.Errorf("targetDeviceId present with %v, want absent", v)
					}
					return
				}
				if !present || v != tc.wantTarget {
					t.Errorf("targetDeviceId = %v, want %q", v, tc.wantTarget)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("timed out waiting for pre-TLS identity")
			}
		})
	}
}
