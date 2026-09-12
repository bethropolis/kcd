package ipc_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/transport"
	"go.uber.org/zap/zaptest"
)

func TestBuildSnapshotCoversOfflineDevices(t *testing.T) {
	logger := zaptest.NewLogger(t)
	devReg := device.NewRegistry(nil)
	pluginReg := plugin.NewRegistry(logger)

	online := device.NewDevice("dev-online", "Online Phone", "phone", logger)
	online.UpdateBattery(50, true)
	online.SetState(device.StatePaired)
	devReg.Add(online)

	offline := device.NewDevice("dev-off", "Offline Phone", "phone", logger)
	offline.SetState(device.StatePaired)
	devReg.Add(offline)

	stranger := device.NewDevice("dev-stranger", "Stranger", "phone", logger)
	devReg.Add(stranger)

	snap := ipc.BuildSnapshot(devReg, pluginReg)
	if len(snap.Devices) != 3 {
		t.Fatalf("expected all 3 devices in snapshot, got %d", len(snap.Devices))
	}

	byID := make(map[string]ipc.DeviceSummary)
	for _, d := range snap.Devices {
		byID[d.ID] = d
	}

	if byID["dev-online"].Battery == nil || byID["dev-online"].Battery.Charge != 50 {
		t.Errorf("online battery missing: %+v", byID["dev-online"].Battery)
	}
	if byID["dev-off"].Battery == nil {
		t.Error("offline paired device should still carry battery (dump parity)")
	}
	if byID["dev-stranger"].Media != nil || byID["dev-stranger"].Signal != nil {
		t.Error("absent plugins must omit media/signal sections")
	}

	// Identity fields survive the enrichment untouched.
	raw, _ := json.Marshal(snap)
	var decoded struct {
		Devices []map[string]interface{} `json:"devices"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, d := range decoded.Devices {
		for _, k := range []string{"id", "name", "type", "state", "connected"} {
			if _, ok := d[k]; !ok {
				t.Errorf("summary missing base field %q: %v", k, d)
			}
		}
	}
}

// A connected device is seen now by definition: last_seen is stamped at
// connect time and discovery sightings skip connected devices, so the raw
// stamp goes stale for the whole session. The summary must report now for
// connected devices and preserve the stored stamp for offline ones.
func TestSummarizeDeviceConnectedMeansSeenNow(t *testing.T) {
	logger := zaptest.NewLogger(t)
	stale := time.Now().Add(-53 * time.Minute)

	offline := device.NewDevice("dev-off", "Offline Phone", "phone", logger)
	offline.SetLastSeen(stale)
	offlineSum := ipc.SummarizeDevice(offline, nil)
	if !offlineSum.LastSeen.Equal(stale) {
		t.Errorf("offline summary must preserve stored last_seen, got %v", offlineSum.LastSeen)
	}

	online := device.NewDevice("dev-on", "Online Phone", "phone", logger)
	online.SetLastSeen(stale)
	left, right := net.Pipe()
	conn := transport.NewConn(tls.Client(left, &tls.Config{InsecureSkipVerify: true}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	online.Connect(ctx, conn, nil, nil, nil)

	onlineSum := ipc.SummarizeDevice(online, nil)
	if time.Since(onlineSum.LastSeen) > time.Minute {
		t.Errorf("connected summary last_seen must be now, got %v", onlineSum.LastSeen)
	}

	_ = right.Close()
	deadline := time.Now().Add(2 * time.Second)
	for online.IsConnected() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	online.Disconnect()
}
