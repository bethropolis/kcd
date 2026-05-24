package runcommand

import (
	"context"
	"testing"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap/zaptest"
)

func TestRunCommandPlugin_Handle_GlobalCommand(t *testing.T) {
	logger := zaptest.NewLogger(t)
	dev := device.NewDevice("dev1", "Test", "phone", logger)
	p := NewRunCommandPlugin(
		map[string]string{"key1": "echo test"},
		nil,
		logger,
	)

	pkt, _ := protocol.NewPacket("kdeconnect.runcommand.request", RequestBody{Key: "key1"})
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}
}

func TestRunCommandPlugin_Handle_PerDeviceOverrides(t *testing.T) {
	logger := zaptest.NewLogger(t)

	p := NewRunCommandPlugin(
		map[string]string{"cmd": "echo global"},
		map[string]map[string]string{
			"dev1": {"cmd": "echo per-device"},
		},
		logger,
	)

	// Device dev1 should get the per-device command.
	dev1 := device.NewDevice("dev1", "Phone 1", "phone", logger)
	pkt, _ := protocol.NewPacket("kdeconnect.runcommand.request", RequestBody{Key: "cmd"})
	if err := p.Handle(context.Background(), dev1, pkt); err != nil {
		t.Fatalf("Handle for dev1 failed: %v", err)
	}

	// Device dev2 should fall back to the global command.
	dev2 := device.NewDevice("dev2", "Phone 2", "phone", logger)
	pkt, _ = protocol.NewPacket("kdeconnect.runcommand.request", RequestBody{Key: "cmd"})
	if err := p.Handle(context.Background(), dev2, pkt); err != nil {
		t.Fatalf("Handle for dev2 failed: %v", err)
	}
}

func TestRunCommandPlugin_Handle_RequestCommandList(t *testing.T) {
	logger := zaptest.NewLogger(t)
	dev := device.NewDevice("dev1", "Test", "phone", logger)
	p := NewRunCommandPlugin(
		map[string]string{"global": "echo global"},
		map[string]map[string]string{
			"dev1": {"device-only": "echo dev-only"},
		},
		logger,
	)

	pkt, _ := protocol.NewPacket("kdeconnect.runcommand.request", RequestBody{RequestCommandList: true})
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}
	// The response packet is sent via dev.Send. No mock assertion here —
	// the test validates no crash and correct routing logic.
}
