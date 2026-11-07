package systemvolume

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/desktop/audio"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/sysexec"
	"github.com/bethropolis/kcd/internal/testutil"
)

type volumeRunner struct{ calls chan []string }

func (r *volumeRunner) LookPath(name string) (string, error) {
	if name == "wpctl" {
		return "/usr/bin/wpctl", nil
	}
	return "", context.DeadlineExceeded
}
func (r *volumeRunner) Output(context.Context, string, ...string) ([]byte, error) {
	return []byte("Volume: 0.40"), nil
}
func (r *volumeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls <- append([]string{name}, args...)
	return nil, nil
}
func (*volumeRunner) RunInput(context.Context, string, string, ...string) ([]byte, error) {
	return nil, nil
}

func TestHandleMuteOnlyLeavesVolumeUntouched(t *testing.T) {
	runner := &volumeRunner{calls: make(chan []string, 4)}
	plugin := NewSystemVolumePlugin(nil, log.Nop(), audio.New(sysexec.New(runner)))
	dev := testutil.NewFakeSender("phone")
	pkt, _ := protocol.NewPacket("kdeconnect.systemvolume.request", json.RawMessage(`{"name":"default","muted":true}`))
	if err := plugin.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatal(err)
	}
	if dev.WaitFor("kdeconnect.systemvolume", time.Second) == nil {
		t.Fatal("updated sink list not sent")
	}
	var calls [][]string
	for {
		select {
		case args := <-runner.calls:
			calls = append(calls, args)
		default:
			goto drained
		}
	}
drained:
	if len(calls) != 1 || !strings.Contains(strings.Join(calls[0], " "), "set-mute") {
		t.Fatalf("mute-only update ran unexpected audio commands: %v", calls)
	}
}

func TestHandleZeroVolumePreservesMuteState(t *testing.T) {
	runner := &volumeRunner{calls: make(chan []string, 4)}
	plugin := NewSystemVolumePlugin(nil, log.Nop(), audio.New(sysexec.New(runner)))
	dev := testutil.NewFakeSender("phone")
	pkt, _ := protocol.NewPacket("kdeconnect.systemvolume.request", json.RawMessage(`{"name":"default","volume":0}`))
	if err := plugin.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatal(err)
	}
	if dev.WaitFor("kdeconnect.systemvolume", time.Second) == nil {
		t.Fatal("updated sink list not sent")
	}
	args := <-runner.calls
	if got := strings.Join(args, " "); got != "wpctl set-volume @DEFAULT_AUDIO_SINK@ 0%" {
		t.Fatalf("volume-only command = %q", got)
	}
	select {
	case extra := <-runner.calls:
		t.Fatalf("volume-only update changed mute state: %v", extra)
	default:
	}
}
