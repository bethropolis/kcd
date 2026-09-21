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
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/transport"
)

func TestBuildSnapshotCoversOfflineDevices(t *testing.T) {
	logger := log.NewTest(t)
	devReg := device.NewRegistry(nil)
	pluginReg := plugin.NewRegistry(logger)

	online := device.NewDevice("dev-online", "Online Phone", "phone", logger)
	online.UpdateBattery(50, true)
	online.SetState(device.StatePaired)
	devReg.Add(online)

	offline := device.NewDevice("dev-off", "Offline Phone", "phone", logger)
	offline.UpdateBattery(24, false)
	offline.SetState(device.StatePaired)
	devReg.Add(offline)

	stranger := device.NewDevice("dev-stranger", "Stranger", "phone", logger)
	devReg.Add(stranger)

	unseen := device.NewDevice("dev-unseen", "Fresh Pair", "phone", logger)
	unseen.SetState(device.StatePaired)
	devReg.Add(unseen)

	snap := ipc.BuildSnapshot(devReg, pluginReg)
	if len(snap.Devices) != 4 {
		t.Fatalf("expected all 4 devices in snapshot, got %d", len(snap.Devices))
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
	if byID["dev-unseen"].Battery != nil {
		t.Errorf("device with no reading must omit battery, got %+v", byID["dev-unseen"].Battery)
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
		if d["id"] == "dev-unseen" {
			if _, ok := d["battery"]; ok {
				t.Errorf("unseen device must serialize without a battery key: %v", d)
			}
		}
	}
}

// Battery omission contract: a fresh device serializes with no battery key
// (unknown, not 0%); one packet makes the key appear with the sent values;
// a real 0% packet is preserved as a present zero, not collapsed to unknown.
func TestSummarizeDeviceBatteryOmission(t *testing.T) {
	logger := log.NewTest(t)
	pluginReg := plugin.NewRegistry(logger)

	fresh := device.NewDevice("dev-fresh", "Fresh", "phone", logger)
	sum := ipc.SummarizeDevice(fresh, pluginReg)
	if sum.Battery != nil {
		t.Fatalf("fresh device must omit battery, got %+v", sum.Battery)
	}
	raw, _ := json.Marshal(sum)
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["battery"]; ok {
		t.Errorf("fresh device must serialize without battery key: %s", raw)
	}

	charged := device.NewDevice("dev-charged", "Charged", "phone", logger)
	charged.UpdateBattery(80, true)
	sum = ipc.SummarizeDevice(charged, pluginReg)
	if sum.Battery == nil {
		t.Fatal("device with a reading must carry battery")
	}
	if sum.Battery.Charge != 80 || !sum.Battery.Charging {
		t.Errorf("battery values wrong: %+v", sum.Battery)
	}
	if sum.Battery.BatteryAgeMs < 0 {
		t.Errorf("battery age must be non-negative after a reading: %+v", sum.Battery)
	}

	dead := device.NewDevice("dev-dead", "Dead", "phone", logger)
	dead.UpdateBattery(0, false)
	sum = ipc.SummarizeDevice(dead, pluginReg)
	if sum.Battery == nil {
		t.Fatal("true 0% reading must still be published")
	}
	if sum.Battery.Charge != 0 || sum.Battery.Charging {
		t.Errorf("true zero values wrong: %+v", sum.Battery)
	}
	raw, _ = json.Marshal(sum)
	decoded = nil
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	bat, ok := decoded["battery"].(map[string]interface{})
	if !ok {
		t.Fatalf("true 0%% must serialize a battery key: %s", raw)
	}
	if bat["charge"] != float64(0) {
		t.Errorf("true 0%% charge wrong: %v", bat)
	}
}

// A connected device is seen now by definition: last_seen is stamped at
// connect time and discovery sightings skip connected devices, so the raw
// stamp goes stale for the whole session. The summary must report now for
// connected devices and preserve the stored stamp for offline ones.
func TestSummarizeDeviceConnectedMeansSeenNow(t *testing.T) {
	logger := log.NewTest(t)
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
