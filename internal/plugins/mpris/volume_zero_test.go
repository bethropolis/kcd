package mpris

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
)

// Zero volume must marshal explicitly: clients read an absent key as
// unknown, which wedges relative controls at exactly 0.
func TestNowPlayingVolumeZeroMarshals(t *testing.T) {
	data, err := json.Marshal(&NowPlaying{Player: "p", Volume: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"volume":0`) {
		t.Fatalf("volume 0 missing from %s", data)
	}
}

// The published mpris.update for a zero-volume state must carry the key.
func TestVolumeZeroPublished(t *testing.T) {
	bus := events.NewBus(log.Nop())
	sub := bus.Subscribe(4, events.TypeMprisUpdate)
	defer sub.Close()

	plugin := NewMPRISPlugin(nil, bus, false, config.MPRISConfig{}, log.Nop())
	if plugin.watchCancel != nil {
		defer plugin.watchCancel()
	}
	dev := testSender{id: "device-vol0"}
	state := MPRISRequest{
		Player:         "Spotify",
		Title:          "Silent Track",
		IsPlaying:      true,
		Volume:         0,
		PlaybackStatus: "Playing",
	}
	if err := plugin.Handle(context.Background(), dev, newMPRISPacket(t, state)); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-sub.C:
		got, ok := ev.Payload.(*NowPlaying)
		if !ok {
			t.Fatalf("expected *NowPlaying payload, got %T", ev.Payload)
		}
		data, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"volume":0`) {
			t.Fatalf("volume 0 missing from published %s", data)
		}
	case <-time.After(time.Second):
		t.Fatal("expected mpris update")
	}
}
