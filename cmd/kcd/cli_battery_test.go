package main

import (
	"context"
	"encoding/json"
	"flag"
	"path/filepath"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/pkg/client"
	"github.com/urfave/cli/v2"
)

// startBatteryStub serves one paired device and a fixed battery reading.
func startBatteryStub(t *testing.T) *client.Client {
	t.Helper()

	sock := filepath.Join(t.TempDir(), "battery.sock")
	logger := log.NewTest(t)

	devs := []device.DeviceInfo{
		{ID: "phone1", Name: "Phone", State: device.StatePaired, Connected: true},
	}
	devReg := device.NewRegistry(nil)
	for _, d := range devs {
		devReg.Add(device.NewDevice(d.ID, d.Name, "phone", logger))
	}

	h := ipc.NewHandler(devReg, plugin.NewRegistry(logger), nil, nil, nil, 0)
	h.Register(ipc.CmdDevices, func(ipc.Request) ipc.Response {
		data, _ := json.Marshal(devs)
		return ipc.Response{OK: true, Data: data}
	})
	h.Register(ipc.CmdBattery, func(ipc.Request) ipc.Response {
		return ipc.Response{OK: true, Data: json.RawMessage(`{"charge":77,"charging":false}`)}
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = ipc.NewServer(sock, h, logger).Listen(ctx) }()
	time.Sleep(100 * time.Millisecond)

	return &client.Client{SocketPath: sock, Timeout: 2 * time.Second}
}

func batteryContext(t *testing.T, args ...string) *cli.Context {
	t.Helper()
	set := flag.NewFlagSet("battery", flag.ContinueOnError)
	set.Bool("json", false, "")
	if err := set.Parse(args); err != nil {
		t.Fatal(err)
	}
	return cli.NewContext(cli.NewApp(), set, nil)
}

func runBatteryJSON(t *testing.T, cl *client.Client, args ...string) map[string]any {
	t.Helper()
	var out map[string]any
	raw := captureStdout(t, func() {
		if err := runBattery(batteryContext(t, args...), cl); err != nil {
			t.Fatalf("runBattery: %v", err)
		}
	})
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("output is not JSON: %q", raw)
	}
	return out
}

// With no positional the query runs against the auto-selected device, so the
// reported ID must be the resolved one rather than the empty raw argument.
func TestBatteryJSONReportsResolvedDeviceID(t *testing.T) {
	cl := startBatteryStub(t)
	out := runBatteryJSON(t, cl, "--json")
	if out["deviceId"] != "phone1" {
		t.Errorf("deviceId = %v, want the auto-resolved phone1", out["deviceId"])
	}
	if out["charge"] != float64(77) {
		t.Errorf("charge = %v, want 77", out["charge"])
	}
	if out["charging"] != false {
		t.Errorf("charging = %v, want false", out["charging"])
	}
}

func TestBatteryJSONExplicitID(t *testing.T) {
	cl := startBatteryStub(t)
	out := runBatteryJSON(t, cl, "--json", "phone1")
	if out["deviceId"] != "phone1" {
		t.Errorf("deviceId = %v, want phone1", out["deviceId"])
	}
}
