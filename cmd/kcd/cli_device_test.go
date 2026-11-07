package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/pkg/client"
	"github.com/urfave/cli/v3"
)

// startDeviceStub runs an IPC server whose devices route returns a fixed
// device list, plus a client wired to it.
func startDeviceStub(t *testing.T, devs []device.DeviceInfo) *client.Client {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	logger := log.NewTest(t)

	devReg := device.NewRegistry(nil)
	for _, d := range devs {
		devReg.Add(device.NewDevice(d.ID, d.Name, "phone", logger))
	}

	handler := ipc.NewHandler(devReg, plugin.NewRegistry(logger), nil)
	handler.Register(ipc.CmdDevices, func(ipc.Request) ipc.Response {
		data, _ := json.Marshal(devs)
		return ipc.Response{OK: true, Data: data}
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = ipc.NewServer(sockPath, handler, logger).Listen(ctx) }()
	time.Sleep(100 * time.Millisecond)

	return &client.Client{SocketPath: sockPath, Timeout: 2 * time.Second}
}

func TestResolveDeviceID(t *testing.T) {
	paired := device.DeviceInfo{ID: "phone1", Name: "Phone", State: device.StatePaired, Connected: true}
	other := device.DeviceInfo{ID: "phone2", Name: "Tablet", State: device.StatePaired, Connected: true}
	unpaired := device.DeviceInfo{ID: "stranger", Name: "Stranger", State: device.StateUnpaired, Connected: true}

	tests := []struct {
		name    string
		devices []device.DeviceInfo
		want    string
		wantErr string
	}{
		{"single paired device auto-resolves", []device.DeviceInfo{paired}, "phone1", ""},
		{"unpaired devices are ignored", []device.DeviceInfo{unpaired}, "", "no paired connected devices"},
		{
			name:    "multiple devices require an explicit ID",
			devices: []device.DeviceInfo{paired, other},
			want:    "",
			// The error must name the candidates so the user can pick.
			wantErr: "phone1, phone2",
		},
		{"no devices errors", nil, "", "no paired connected devices"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cl := startDeviceStub(t, tc.devices)
			var got string
			var resolveErr error
			cmd := &cli.Command{
				Action: func(ctx context.Context, cmd *cli.Command) error {
					got, resolveErr = resolveDeviceID(cmd, cl)
					return nil
				},
			}
			_ = cmd.Run(context.Background(), []string{"test"})

			if tc.wantErr != "" {
				if resolveErr == nil {
					t.Fatalf("expected error containing %q, got nil (resolved %q)", tc.wantErr, got)
				}
				if !strings.Contains(resolveErr.Error(), tc.wantErr) {
					t.Errorf("error %q does not contain %q", resolveErr, tc.wantErr)
				}
				return
			}
			if resolveErr != nil {
				t.Fatalf("unexpected error: %v", resolveErr)
			}
			if got != tc.want {
				t.Errorf("resolved %q, want %q", got, tc.want)
			}
		})
	}
}

// An explicit positional always wins over auto-resolution, even when
// several devices are connected.
func TestResolveDeviceIDExplicitArgumentWins(t *testing.T) {
	cl := startDeviceStub(t, []device.DeviceInfo{
		{ID: "phone1", State: device.StatePaired, Connected: true},
		{ID: "phone2", State: device.StatePaired, Connected: true},
	})

	var got string
	var resolveErr error
	cmd := &cli.Command{
		Action: func(ctx context.Context, cmd *cli.Command) error {
			got, resolveErr = resolveDeviceID(cmd, cl)
			return nil
		},
	}
	if err := cmd.Run(context.Background(), []string{"test", "phone2"}); err != nil {
		t.Fatal(err)
	}
	if resolveErr != nil {
		t.Fatalf("unexpected error: %v", resolveErr)
	}
	if got != "phone2" {
		t.Errorf("resolved %q, want the explicit phone2", got)
	}
}
