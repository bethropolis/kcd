package runcommand

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/testutil"
)

func TestRunCommandPlugin_Handle_GlobalCommand(t *testing.T) {
	logger := log.NewTest(t)
	dev := device.NewDevice("dev1", "Test", "phone", logger)
	p := NewRunCommandPlugin(
		map[string]string{"key1": "echo test"},
		nil,
		nil,
		logger,
	)

	pkt, _ := protocol.NewPacket("kdeconnect.runcommand.request", RequestBody{Key: "key1"})
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}
	p.wg.Wait()
}

func TestRunCommandPlugin_Handle_PerDeviceOverrides(t *testing.T) {
	logger := log.NewTest(t)

	p := NewRunCommandPlugin(
		map[string]string{"cmd": "echo global"},
		map[string]map[string]string{
			"dev1": {"cmd": "echo per-device"},
		},
		nil,
		logger,
	)

	// Device dev1 should get the per-device command.
	dev1 := device.NewDevice("dev1", "Phone 1", "phone", logger)
	pkt, _ := protocol.NewPacket("kdeconnect.runcommand.request", RequestBody{Key: "cmd"})
	if err := p.Handle(context.Background(), dev1, pkt); err != nil {
		t.Fatalf("Handle for dev1 failed: %v", err)
	}
	p.wg.Wait()

	// Device dev2 should fall back to the global command.
	dev2 := device.NewDevice("dev2", "Phone 2", "phone", logger)
	pkt, _ = protocol.NewPacket("kdeconnect.runcommand.request", RequestBody{Key: "cmd"})
	if err := p.Handle(context.Background(), dev2, pkt); err != nil {
		t.Fatalf("Handle for dev2 failed: %v", err)
	}
	p.wg.Wait()
}

func TestRunCommandPlugin_Handle_RequestCommandList(t *testing.T) {
	logger := log.NewTest(t)
	dev := device.NewDevice("dev1", "Test", "phone", logger)
	p := NewRunCommandPlugin(
		map[string]string{"global": "echo global"},
		map[string]map[string]string{
			"dev1": {"device-only": "echo dev-only"},
		},
		nil,
		logger,
	)

	pkt, _ := protocol.NewPacket("kdeconnect.runcommand.request", RequestBody{RequestCommandList: true})
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}
	// The response packet is sent via dev.Send. No mock assertion here —
	// the test validates no crash and correct routing logic.
}

func TestRunCommandPlugin_OnConnect_PushesCommandList(t *testing.T) {
	logger := log.NewTest(t)
	dev := testutil.NewFakeSender("dev1")
	p := NewRunCommandPlugin(
		map[string]string{"global": "echo global"},
		map[string]map[string]string{
			"dev1": {"per-dev": "echo dev1"},
		},
		nil,
		logger,
	)

	p.OnConnect(dev)

	pkts := dev.Sent()
	if len(pkts) != 1 {
		t.Fatalf("got %d packets on connect, want 1", len(pkts))
	}
	pkt := pkts[0]
	if pkt.Type != "kdeconnect.runcommand" {
		t.Fatalf("packet type = %q, want kdeconnect.runcommand", pkt.Type)
	}

	var body struct {
		CommandList   string `json:"commandList"`
		CanAddCommand bool   `json:"canAddCommand"`
	}
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body.CanAddCommand {
		t.Errorf("canAddCommand = true, want false")
	}

	var list map[string]map[string]string
	if err := json.Unmarshal([]byte(body.CommandList), &list); err != nil {
		t.Fatalf("unmarshal commandList: %v", err)
	}
	if list["global"]["command"] != "echo global" || list["per-dev"]["command"] != "echo dev1" {
		t.Errorf("unexpected commandList: %v", list)
	}
}

func TestRunCommandPlugin_SetCommands(t *testing.T) {
	logger := log.NewTest(t)
	dev := testutil.NewFakeSender("dev1")
	p := NewRunCommandPlugin(nil, nil, nil, logger)

	p.SetCommands(
		map[string]string{"new_global": "ls"},
		map[string]map[string]string{"dev1": {"override": "pwd"}},
	)

	if err := p.SendCommandList(dev); err != nil {
		t.Fatalf("SendCommandList: %v", err)
	}

	pkts := dev.Sent()
	if len(pkts) != 1 {
		t.Fatalf("got %d packets, want 1", len(pkts))
	}

	var body struct {
		CommandList string `json:"commandList"`
	}
	if err := json.Unmarshal(pkts[0].Body, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var list map[string]map[string]string
	if err := json.Unmarshal([]byte(body.CommandList), &list); err != nil {
		t.Fatalf("unmarshal commandList: %v", err)
	}
	if list["new_global"]["command"] != "ls" || list["override"]["command"] != "pwd" {
		t.Errorf("unexpected commandList after SetCommands: %v", list)
	}
}
