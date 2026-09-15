package device

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

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
