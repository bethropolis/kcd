package main

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

// startMprisStub runs an IPC server whose mpris remote route returns
// players, and returns a client wired to it.
func startMprisStub(t *testing.T, players []ipc.MprisRemotePlayer) *client.Client {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	logger := log.NewTest(t)

	handler := ipc.NewHandler(device.NewRegistry(nil), plugin.NewRegistry(logger), nil, "", nil, 0)
	handler.Register(ipc.CmdMprisRemote, func(ipc.Request) ipc.Response {
		data, _ := json.Marshal(ipc.MprisRemoteResponse{Players: players})
		return ipc.Response{OK: true, Data: data}
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = ipc.NewServer(sockPath, handler, logger).Listen(ctx) }()
	time.Sleep(100 * time.Millisecond)

	return &client.Client{SocketPath: sockPath, Timeout: 2 * time.Second}
}

// A paused player must report false so the skip leaves it paused; a
// playing one must report true so the skip can resume the next track.
func TestRemotePlayerPlaying(t *testing.T) {
	players := []ipc.MprisRemotePlayer{
		{DeviceID: "dev1", Player: "Spotify", IsPlaying: true},
		{DeviceID: "dev2", Player: "Metrolist", IsPlaying: false},
	}
	cl := startMprisStub(t, players)

	tests := []struct {
		name     string
		deviceID string
		player   string
		want     bool
	}{
		{"playing player", "dev1", "Spotify", true},
		{"paused player", "dev2", "Metrolist", false},
		{"empty player name takes the device's player", "dev2", "", false},
		{"empty device and player takes the first", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := remotePlayerPlaying(cl, tc.deviceID, tc.player); got != tc.want {
				t.Errorf("remotePlayerPlaying(%q, %q) = %v, want %v", tc.deviceID, tc.player, got, tc.want)
			}
		})
	}
}

// State the daemon cannot report must default to true: dropping the Play
// nudge would regress the phones that stop after a skip.
func TestRemotePlayerPlayingUnknownDefaultsToNudge(t *testing.T) {
	cl := startMprisStub(t, []ipc.MprisRemotePlayer{
		{DeviceID: "dev1", Player: "Spotify", IsPlaying: false},
	})

	if !remotePlayerPlaying(cl, "dev1", "NoSuchPlayer") {
		t.Error("an unmatched player must default to true (nudge), got false")
	}
	if !remotePlayerPlaying(cl, "unknown-device", "") {
		t.Error("an unknown device must default to true (nudge), got false")
	}
}
