package ipc_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/pkg/client"
)

func TestIPCRoundTrip(t *testing.T) {
	// Setup
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	logger := log.NewTest(t)

	devReg := device.NewRegistry(nil)
	pluginReg := plugin.NewRegistry(logger)
	dev1 := device.NewDevice("dev123", "Pixel 5", "phone", logger)
	devReg.Add(dev1)

	handler := ipc.NewHandler(devReg, pluginReg, nil)
	handler.Register(ipc.CmdDevices, func(req ipc.Request) ipc.Response {
		data, _ := json.Marshal([]ipc.DeviceSummary{{DeviceInfo: dev1.Info()}})
		return ipc.Response{OK: true, Data: data}
	})
	handler.Register(ipc.CmdPair, func(req ipc.Request) ipc.Response {
		var p ipc.PairPayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		if _, ok := devReg.Get(p.DeviceID); !ok {
			return ipc.Response{OK: false, Error: "device not found"}
		}
		return ipc.Response{OK: true}
	})
	handler.Register(ipc.CmdPing, func(req ipc.Request) ipc.Response {
		return ipc.Response{OK: true}
	})
	handler.Register(ipc.CmdUnpair, func(req ipc.Request) ipc.Response {
		var p ipc.DevicePayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		devReg.Remove(p.DeviceID)
		return ipc.Response{OK: true}
	})
	server := ipc.NewServer(sockPath, handler, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run Server
	go func() {
		_ = server.Listen(ctx)
	}()

	// Wait for socket
	time.Sleep(100 * time.Millisecond)

	cl := &client.Client{SocketPath: sockPath, Timeout: 2 * time.Second}

	// Test 1: Devices
	devs, err := cl.Devices()
	if err != nil {
		t.Fatalf("Devices() failed: %v", err)
	}
	if len(devs) != 1 || devs[0].ID != "dev123" {
		t.Errorf("Expected 1 device 'dev123', got %v", devs)
	}

	// Test 2: Pair (device exists)
	if _, err := cl.Pair("dev123"); err != nil {
		t.Errorf("Pair() failed: %v", err)
	}

	// Test 3: Ping
	if err := cl.Ping("dev123"); err != nil {
		t.Errorf("Ping() failed: %v", err)
	}

	// Test 4: Unpair
	if err := cl.Unpair("dev123"); err != nil {
		t.Errorf("Unpair() failed: %v", err)
	}

	// Device should be removed
	if _, ok := devReg.Get("dev123"); ok {
		t.Error("expected device to be removed from registry after unpair")
	}

	// Test 5: Pair (unknown device)
	_, err = cl.Pair("unknown")
	if err == nil {
		t.Error("expected Pair() to fail for unknown device")
	}
}
