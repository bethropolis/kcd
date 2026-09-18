package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/ipc"
)

func TestFormatStatus(t *testing.T) {
	st := &ipc.StatusResponse{
		Version:        "v1.19.0",
		UptimeHuman:    "2h 14m",
		SocketPath:     "/run/user/1000/kcd/kcd.sock",
		ConfigPath:     "/home/user/.config/kcd/kcd.toml",
		TCPPort:        1716,
		Plugins:        []string{"Pair", "Battery"},
		DeviceCount:    2,
		ConnectedCount: 1,
		Devices: []ipc.StatusDevice{
			{
				ID: "9a5c23ea_7195_4da1_b766_282b7256a02d", Name: "BETHRÖ",
				Type: "phone", State: "PAIRED", Connected: true,
				Addr:     "192.168.1.134:1716",
				Battery:  &ipc.StatusBattery{Charge: 78, Charging: true},
				LastSeen: time.Now().Add(-3 * time.Second).UTC().Format(time.RFC3339),
			},
			{
				ID: "deadbeef_0000_0000_0000_000000000000", Name: "Old Laptop",
				Type: "laptop", State: "UNPAIRED", Connected: false,
			},
		},
	}
	out := formatStatus(st)
	for _, want := range []string{
		"kcd v1.19.0 (up 2h 14m)",
		"\nSocket:   /run/user/1000/kcd/kcd.sock",
		"Listen:   tcp :1716",
		"\nDevices:  2 known, 1 connected",
		"BETHRÖ", "9a5c23ea", "PAIRED", "192.168.1.134:1716", "78%+",
		"Old Laptop", "UNPAIRED", "—", "never",
		"Plugins (2): Pair, Battery",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatSeenAge(t *testing.T) {
	if got := formatSeenAge(""); got != "never" {
		t.Errorf("empty = %q, want never", got)
	}
	if got := formatSeenAge("not-a-time"); got != "not-a-time" {
		t.Errorf("garbage = %q, want passthrough", got)
	}
	if got := formatSeenAge(time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339)); got != "1m ago" {
		t.Errorf("90s = %q, want 1m ago", got)
	}
}
