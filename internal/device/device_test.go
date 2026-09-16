package device

import (
	"context"
	"crypto/tls"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
	"go.uber.org/zap/zaptest"
)

func TestRegistry_Deduplicate(t *testing.T) {
	reg := NewRegistry(nil)
	logger := zaptest.NewLogger(t)

	d1 := NewDevice("123", "Phone 1", "phone", logger)
	d2 := NewDevice("123", "Phone 2", "phone", logger)

	reg.Add(d1)
	reg.Add(d2)

	devices := reg.List()
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}

	if devices[0].Name() != "Phone 2" {
		t.Errorf("expected updated name 'Phone 2', got %q", devices[0].Name())
	}
}

func TestReconnectBackoff(t *testing.T) {
	max := 60 * time.Second
	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 2 * time.Second},
		{1, 4 * time.Second},
		{2, 8 * time.Second},
		{3, 16 * time.Second},
		{5, 60 * time.Second}, // Caps out
		{10, 60 * time.Second},
	}

	for _, tt := range tests {
		actual := ReconnectBackoff(tt.attempt, max)
		if actual != tt.expected {
			t.Errorf("attempt %d: expected %v, got %v", tt.attempt, tt.expected, actual)
		}
	}
}

func TestDevice_ReconnectAttempt(t *testing.T) {
	logger := zaptest.NewLogger(t)
	d := NewDevice("123", "Phone", "phone", logger)

	if got := d.ReconnectAttempt(); got != 0 {
		t.Fatalf("expected initial attempt 0, got %d", got)
	}

	d.SetReconnectAttempt(3)
	if got := d.ReconnectAttempt(); got != 3 {
		t.Fatalf("expected attempt 3, got %d", got)
	}

	d.ResetReconnectAttempt()
	if got := d.ReconnectAttempt(); got != 0 {
		t.Fatalf("expected attempt 0 after reset, got %d", got)
	}
}

func TestDevice_ConnectionAge(t *testing.T) {
	logger := zaptest.NewLogger(t)
	d := NewDevice("123", "Phone", "phone", logger)

	if got := d.ConnectionAge(); got != 0 {
		t.Fatalf("expected 0 for never-connected device, got %v", got)
	}

	// Connect with a pipe-based TLS connection (no handshake required).
	left, right := net.Pipe()
	conn := transport.NewConn(tls.Client(left, &tls.Config{InsecureSkipVerify: true}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.Connect(ctx, conn, nil, nil, nil)

	if got := d.ConnectionAge(); got <= 0 {
		t.Fatalf("expected positive age after connect, got %v", got)
	}

	time.Sleep(20 * time.Millisecond)
	if got := d.ConnectionAge(); got < 20*time.Millisecond {
		t.Errorf("expected age to grow past 20ms, got %v", got)
	}

	// Tear down: closing the pipe unblocks readLoop, which clears the
	// connection. Wait for it so no goroutine logs after the test returns.
	_ = right.Close()
	deadline := time.Now().Add(2 * time.Second)
	for d.IsConnected() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	d.Disconnect()
}

func TestDevice_BatterySeen(t *testing.T) {
	logger := zaptest.NewLogger(t)
	d := NewDevice("bat-seen", "Phone", "phone", logger)

	if d.HasBattery() {
		t.Error("fresh device must report no battery reading")
	}
	if got := d.BatteryAge(); got >= 0 {
		t.Errorf("fresh device battery age must be negative, got %v", got)
	}

	d.UpdateBattery(80, true)
	if !d.HasBattery() {
		t.Error("device must report a reading after UpdateBattery")
	}
	if charge, charging := d.GetBattery(); charge != 80 || !charging {
		t.Errorf("GetBattery = (%d, %v), want (80, true)", charge, charging)
	}
	if got := d.BatteryAge(); got < 0 {
		t.Errorf("battery age must be non-negative after a reading, got %v", got)
	}

	// A true zero is a real reading, not an unknown.
	d2 := NewDevice("bat-zero", "Phone", "phone", logger)
	d2.UpdateBattery(0, false)
	if !d2.HasBattery() {
		t.Error("true 0% reading must count as seen")
	}
}

func TestDeviceInfoDialTargetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/devices.json"

	infos := []DeviceInfo{
		{ID: "paired-1", Name: "Phone", Type: "phone", State: StatePaired, LastIP: "192.168.1.20", LastPort: 1716},
		{ID: "fresh-1", Name: "New", Type: "phone", State: StateUnpaired},
	}
	if err := SaveDevices(path, infos); err != nil {
		t.Fatalf("SaveDevices failed: %v", err)
	}
	loaded, err := LoadDevices(path)
	if err != nil {
		t.Fatalf("LoadDevices failed: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(loaded))
	}

	ip, port := loaded[0].DialTarget()
	if ip == nil || ip.String() != "192.168.1.20" || port != 1716 {
		t.Errorf("dial target not preserved: ip=%v port=%d", ip, port)
	}
	if ip, port := loaded[1].DialTarget(); ip != nil || port != 0 {
		t.Errorf("device without target must yield nil/0, got %v/%d", ip, port)
	}
}

func TestDeviceInfoDialTargetRejectsGarbage(t *testing.T) {
	cases := []struct {
		name     string
		info     DeviceInfo
		wantIP   bool
		wantPort int
	}{
		{"valid", DeviceInfo{LastIP: "10.0.0.5", LastPort: 1716}, true, 1716},
		{"garbage ip", DeviceInfo{LastIP: "not-an-ip", LastPort: 1716}, false, 0},
		{"empty ip", DeviceInfo{LastPort: 1716}, false, 0},
		{"hostname not ip", DeviceInfo{LastIP: "phone.local", LastPort: 1716}, false, 0},
		{"zero port falls back", DeviceInfo{LastIP: "10.0.0.5"}, true, 0},
		{"port out of range", DeviceInfo{LastIP: "10.0.0.5", LastPort: 99999}, true, 0},
		{"negative port", DeviceInfo{LastIP: "10.0.0.5", LastPort: -1}, true, 0},
		{"ipv6", DeviceInfo{LastIP: "fd00::5", LastPort: 1716}, true, 1716},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip, port := tc.info.DialTarget()
			if (ip != nil) != tc.wantIP || port != tc.wantPort {
				t.Errorf("DialTarget() = (%v, %d), want ip-present=%v port=%d",
					ip, port, tc.wantIP, tc.wantPort)
			}
		})
	}
}

// pipeConn builds a transport.Conn over an in-memory pipe without a TLS
// handshake. Closing peer tears the session down through the readLoop,
// exactly like a dropped TCP connection.
func pipeConn(t *testing.T) (*transport.Conn, net.Conn) {
	t.Helper()
	left, right := net.Pipe()
	return transport.NewConn(tls.Client(left, &tls.Config{InsecureSkipVerify: true})), right
}

func waitConnected(t *testing.T, d *Device, want bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for d.IsConnected() != want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if d.IsConnected() != want {
		t.Fatalf("expected connected=%v", want)
	}
}

// waitCounter polls an atomic counter to a value. Callbacks fire on device
// goroutines, so tests must never read plain ints set from them.
func waitCounter(t *testing.T, c *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for c.Load() != want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if c.Load() != want {
		t.Fatalf("expected counter %d, got %d", want, c.Load())
	}
}

// quiesce drains every session loop so no goroutine logs after the test
// returns (zaptest panics on late logs).
func quiesce(t *testing.T, d *Device) {
	t.Helper()
	d.mu.RLock()
	quiet := d.conn == nil
	d.mu.RUnlock()
	deadline := time.Now().Add(2 * time.Second)
	for !quiet && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		d.mu.RLock()
		quiet = d.conn == nil
		d.mu.RUnlock()
	}
	time.Sleep(100 * time.Millisecond)
}

// readAny asserts the writer loop delivers bytes to peer within the deadline.
func readAny(t *testing.T, peer net.Conn) {
	t.Helper()
	_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, err := peer.Read(buf)
	_ = peer.SetReadDeadline(time.Time{})
	if err != nil {
		t.Fatalf("expected bytes from writer loop, got error: %v", err)
	}
	if n == 0 {
		t.Fatal("expected bytes from writer loop, got 0")
	}
}

func TestDevice_ReplaceOnNewAuth(t *testing.T) {
	logger := zaptest.NewLogger(t)
	d := NewDevice("dup", "Phone", "phone", logger)
	var connects, disconnects atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, rightA := pipeConn(t)
	d.Connect(ctx, a, nil,
		func(*Device) { connects.Add(1) },
		func(*Device) { disconnects.Add(1) })
	// Second session while one is live: replaces the dead socket
	// immediately (matching LanDeviceLink::reset).
	b, rightB := pipeConn(t)
	d.Connect(ctx, b, nil,
		func(*Device) { connects.Add(1) },
		func(*Device) { disconnects.Add(1) })

	if !d.IsConnected() {
		t.Fatal("device must stay connected after replace")
	}

	// Old peer must see EOF — old socket was closed.
	_ = rightA.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := rightA.Read(make([]byte, 1)); err == nil {
		t.Fatal("old connection must be closed on replace")
	}
	_ = rightA.Close()

	// No disconnect event for the superseded session.
	time.Sleep(50 * time.Millisecond)
	if disconnects.Load() != 0 {
		t.Fatalf("superseded disconnect must not fire onDisconnect, got %d", disconnects.Load())
	}

	// Writer must serve the new session.
	pkt, err := protocol.NewPacket("kdeconnect.ping", map[string]any{})
	if err != nil {
		t.Fatalf("build ping packet: %v", err)
	}
	if err := d.Send(pkt); err != nil {
		t.Fatalf("Send after replace: %v", err)
	}
	readAny(t, rightB)

	_ = rightB.Close()
	d.Disconnect()
	waitCounter(t, &disconnects, 1)
	quiesce(t, d)
}

func TestDevice_SupersededDisconnectSilent(t *testing.T) {
	logger := zaptest.NewLogger(t)
	d := NewDevice("sup", "Phone", "phone", logger)
	var disconnects atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, rightA := pipeConn(t)
	d.Connect(ctx, a, nil, nil, func(*Device) { disconnects.Add(1) })
	b, rightB := pipeConn(t)
	d.Connect(ctx, b, nil, nil, func(*Device) { disconnects.Add(1) })

	// The first session was already superseded; its peer closing is silent.
	_ = rightA.Close()
	time.Sleep(80 * time.Millisecond)
	if disconnects.Load() != 0 {
		t.Fatalf("superseded session death must not fire onDisconnect, got %d", disconnects.Load())
	}
	if !d.IsConnected() {
		t.Fatal("replacement session must remain connected")
	}

	// Closing the current session fires the single disconnect.
	_ = rightB.Close()
	waitCounter(t, &disconnects, 1)
	waitConnected(t, d, false)
	quiesce(t, d)
}

func TestDevice_ReplaceClosesOld(t *testing.T) {
	logger := zaptest.NewLogger(t)
	d := NewDevice("rep", "Phone", "phone", logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, rightA := pipeConn(t)
	d.Connect(ctx, a, nil, nil, nil)
	b, rightB := pipeConn(t)
	defer rightB.Close()
	d.Connect(ctx, b, nil, nil, nil)
	c, rightC := pipeConn(t)
	defer rightC.Close()
	d.Connect(ctx, c, nil, nil, nil)

	// Each replace closes the previous socket; oldest peer sees EOF.
	_ = rightA.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := rightA.Read(make([]byte, 1)); err == nil {
		t.Fatal("oldest connection must be closed on replace")
	}
	_ = rightA.Close()

	// Writer must serve the newest session.
	pkt, err := protocol.NewPacket("kdeconnect.ping", map[string]any{})
	if err != nil {
		t.Fatalf("build ping packet: %v", err)
	}
	if err := d.Send(pkt); err != nil {
		t.Fatalf("Send after replace: %v", err)
	}
	readAny(t, rightC)

	d.Disconnect()
	waitConnected(t, d, false)
	quiesce(t, d)
}

func TestDevice_CooldownWindow(t *testing.T) {
	logger := zaptest.NewLogger(t)
	d := NewDevice("cd", "Phone", "phone", logger)

	if d.InCooldown() {
		t.Fatal("fresh device must not be in cooldown")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, rightA := pipeConn(t)
	defer rightA.Close()
	d.Connect(ctx, a, nil, nil, nil)

	if !d.InCooldown() {
		t.Fatal("device must be in cooldown right after connect")
	}

	// White-box time travel past the window.
	d.mu.Lock()
	d.lastConnect = time.Now().Add(-(reconnectCooldown + time.Second))
	d.mu.Unlock()
	if d.InCooldown() {
		t.Fatal("cooldown must lapse after the window")
	}
	d.Disconnect()
	quiesce(t, d)
}

func TestDevice_NoteSightingRoamConfirm(t *testing.T) {
	logger := zaptest.NewLogger(t)
	d := NewDevice("roam", "Phone", "phone", logger)
	d.SetLastIP(net.ParseIP("192.168.1.10"))

	other := net.ParseIP("192.168.1.20")
	if d.NoteSighting(other) {
		t.Fatal("single sighting must not confirm a roam")
	}
	if !d.NoteSighting(other) {
		t.Fatal("repeated sighting at a new address must confirm a roam")
	}
	// Wobble back: home address never confirms.
	if d.NoteSighting(net.ParseIP("192.168.1.10")) {
		t.Fatal("sighting at the known address must not confirm a roam")
	}
	// Alternation never confirms either.
	if d.NoteSighting(other) {
		t.Fatal("wobble must not confirm a roam")
	}
}
