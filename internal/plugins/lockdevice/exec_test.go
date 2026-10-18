package lockdevice

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/sysexec"
)

type recordingRunner struct{ calls chan string }

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls <- name + " " + args[0]
	return nil, nil
}
func (*recordingRunner) Output(context.Context, string, ...string) ([]byte, error) { return nil, nil }
func (*recordingRunner) RunInput(context.Context, string, string, ...string) ([]byte, error) {
	return nil, nil
}
func (*recordingRunner) LookPath(string) (string, error) { return "", errors.New("unused") }

func TestInjectedRunnerReceivesLockAndUnlock(t *testing.T) {
	runner := &recordingRunner{calls: make(chan string, 2)}
	p := NewLockDevicePlugin(log.Nop(), sysexec.New(runner))
	dev := &captureSender{}
	if err := p.Handle(context.Background(), dev, lockPacket(t, "kdeconnect.lock.request", LockBody{SetLocked: true})); err != nil {
		t.Fatal(err)
	}
	if err := p.Handle(context.Background(), dev, lockPacket(t, "kdeconnect.lock.request", LockBody{SetLocked: false})); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"loginctl lock-session": false, "loginctl unlock-session": false}
	for range 2 {
		select {
		case got := <-runner.calls:
			if _, ok := want[got]; !ok {
				t.Fatalf("unexpected invocation %q", got)
			}
			want[got] = true
		case <-time.After(time.Second):
			t.Fatal("injected runner did not receive both calls")
		}
	}
	for call, seen := range want {
		if !seen {
			t.Errorf("missing %q", call)
		}
	}
}
