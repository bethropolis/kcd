package ipc_test

import (
	"encoding/json"
	"testing"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
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
