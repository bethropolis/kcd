package mpris

import (
	"encoding/json"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestBuildNowPlayingUsesUnknownVolumeWhenUnsupported(t *testing.T) {
	state := buildNowPlaying(map[string]dbus.Variant{
		"PlaybackStatus": dbus.MakeVariant("Paused"),
	}, "player")

	if state.Volume != -1 {
		t.Fatalf("unsupported volume = %d, want -1", state.Volume)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" {
		t.Fatal("expected serialized state")
	}
}

func TestBuildNowPlayingStoresTrackID(t *testing.T) {
	state := buildNowPlaying(map[string]dbus.Variant{
		"Metadata": dbus.MakeVariant(map[string]dbus.Variant{
			"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/org/mpris/MediaPlayer2/Track/1")),
		}),
	}, "player")

	if state.trackID != "/org/mpris/MediaPlayer2/Track/1" {
		t.Fatalf("track ID = %q", state.trackID)
	}
}
