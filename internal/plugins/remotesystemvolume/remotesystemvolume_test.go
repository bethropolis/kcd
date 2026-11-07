package remotesystemvolume

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/testutil"
)

func TestPartialMuteUpdatePreservesCachedVolume(t *testing.T) {
	p := NewRemoteSystemVolumePlugin(nil, log.Nop())
	p.sinks.Store("phone", []SinkInfo{{Name: "sink", Volume: 42, Muted: true}})
	pkt, _ := protocol.NewPacket("kdeconnect.systemvolume", json.RawMessage(`{"name":"sink","muted":false}`))
	if err := p.Handle(context.Background(), testutil.NewFakeSender("phone"), pkt); err != nil {
		t.Fatal(err)
	}
	got := p.ListSinks("phone")[0]
	if got.Volume != 42 || got.Muted {
		t.Fatalf("partial update overwrote cached fields: %+v", got)
	}
}

func TestOutboundZeroAndFalseAreSerialized(t *testing.T) {
	p := NewRemoteSystemVolumePlugin(nil, log.Nop())
	dev := testutil.NewFakeSender("phone")
	if err := p.SetVolume(dev, "sink", 0); err != nil {
		t.Fatal(err)
	}
	if err := p.SetMuted(dev, "sink", false); err != nil {
		t.Fatal(err)
	}
	sent := dev.Sent()
	for i, want := range []string{`"volume":0`, `"muted":false`} {
		var body map[string]any
		if err := json.Unmarshal(sent[i].Body, &body); err != nil {
			t.Fatal(err)
		}
		key := "volume"
		if i == 1 {
			key = "muted"
		}
		if body[key] == nil {
			t.Fatalf("outbound request omitted %s: %s", want, sent[i].Body)
		}
	}
}
