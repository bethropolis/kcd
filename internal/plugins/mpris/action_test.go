package mpris

import (
	"encoding/json"
	"testing"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

func TestCanonicalAction(t *testing.T) {
	for in, want := range map[string]string{
		"playpause": "PlayPause",
		"PlayPause": "PlayPause",
		"PLAY":      "Play",
		"pause":     "Pause",
		"next":      "Next",
		"previous":  "Previous",
		"stop":      "Stop",
		"raise":     "raise", // unknown: unchanged
		"":          "",
	} {
		if got := CanonicalAction(in); got != want {
			t.Errorf("CanonicalAction(%q) = %q, want %q", in, got, want)
		}
	}
}

type captureSender struct {
	testSender
	sent []*protocol.Packet
}

func (s *captureSender) Send(p *protocol.Packet) error { s.sent = append(s.sent, p); return nil }

func ptr[T any](v T) *T { return &v }

// A relative seek is sent as SetPosition.
func TestSendActionSendsSeekAsSetPosition(t *testing.T) {
	p := NewMPRISPlugin(nil, events.NewBus(log.Nop()), false, config.MPRISConfig{}, log.Nop())
	p.remoteStates["phone"] = &NowPlaying{Player: "music", Pos: 60_000, Length: 200_000}

	for _, tc := range []struct {
		name         string
		seek, setPos *int64
		wantPos      int64
	}{
		{"forward", ptr[int64](30_000), nil, 90_000},
		{"clamped at start", ptr[int64](-90_000), nil, 0},
		{"clamped at end", ptr[int64](500_000), nil, 200_000},
		{"absolute wins", ptr[int64](30_000), ptr[int64](5_000), 5_000},
	} {
		dev := &captureSender{testSender: testSender{id: "phone"}}
		if err := p.SendAction(dev, "music", "", tc.seek, tc.setPos, nil); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := len(dev.sent); got != 1 {
			t.Fatalf("%s: sent %d packets, want 1", tc.name, got)
		}
		var body map[string]any
		if err := json.Unmarshal(dev.sent[0].Body, &body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["Seek"]; ok {
			t.Errorf("%s: Seek sent alongside SetPosition: %v", tc.name, body)
		}
		if got, _ := body["SetPosition"].(float64); int64(got) != tc.wantPos {
			t.Errorf("%s: SetPosition = %v, want %d", tc.name, body["SetPosition"], tc.wantPos)
		}
	}
}

// With no tracked position there is nothing to resolve a relative seek
// against, and phones ignore Seek — so sending it would report success while
// doing nothing. The call must fail loudly instead.
func TestSendActionRelativeSeekWithoutStateErrors(t *testing.T) {
	p := NewMPRISPlugin(nil, events.NewBus(log.Nop()), false, config.MPRISConfig{}, log.Nop())

	dev := &captureSender{testSender: testSender{id: "phone"}}
	err := p.SendAction(dev, "music", "", ptr[int64](30_000), nil, nil)
	if err == nil {
		t.Fatal("SendAction with a relative seek and no tracked state = nil error, want error")
	}
	if len(dev.sent) != 0 {
		t.Errorf("sent %d packets, want 0: a seek that cannot be resolved must not go out", len(dev.sent))
	}
}

// An absolute setPosition needs no tracked state: the phone already knows
// where it is, so it must still work with nothing cached.
func TestSendActionAbsoluteSeekNeedsNoState(t *testing.T) {
	p := NewMPRISPlugin(nil, events.NewBus(log.Nop()), false, config.MPRISConfig{}, log.Nop())

	dev := &captureSender{testSender: testSender{id: "phone"}}
	if err := p.SendAction(dev, "music", "", nil, ptr[int64](42_000), nil); err != nil {
		t.Fatalf("absolute setPosition with no tracked state: %v", err)
	}
	if len(dev.sent) == 0 {
		t.Fatal("no packet sent")
	}
	var body map[string]any
	if err := json.Unmarshal(dev.sent[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if got, _ := body["SetPosition"].(float64); int64(got) != 42_000 {
		t.Errorf("SetPosition = %v, want 42000", body["SetPosition"])
	}
}
