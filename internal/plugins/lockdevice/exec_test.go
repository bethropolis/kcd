package lockdevice

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/sysexec"
	"github.com/bethropolis/kcd/internal/testutil"
)

type recordingRunner struct {
	calls  chan string
	locked bool
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls <- name + " " + args[0]
	return nil, nil
}
func (r *recordingRunner) Output(context.Context, string, ...string) ([]byte, error) {
	if r.locked {
		return []byte("LockedHint=yes"), nil
	}
	return []byte("LockedHint=no"), nil
}
func (*recordingRunner) RunInput(context.Context, string, string, ...string) ([]byte, error) {
	return nil, nil
}
func (*recordingRunner) LookPath(string) (string, error) { return "", errors.New("unused") }

func TestInjectedRunnerReceivesLockAndUnlock(t *testing.T) {
	runner := &recordingRunner{calls: make(chan string, 2)}
	p := NewLockDevicePlugin(config.LockDeviceConfig{AllowRemoteUnlock: true}, nil, log.Nop(), sysexec.New(runner))
	dev := testutil.NewFakeSender("device")
	locked := true
	if err := p.Handle(context.Background(), dev, lockPacket(t, "kdeconnect.lock.request", inboundLockRequest{SetLocked: &locked})); err != nil {
		t.Fatal(err)
	}
	locked = false
	if err := p.Handle(context.Background(), dev, lockPacket(t, "kdeconnect.lock.request", inboundLockRequest{SetLocked: &locked})); err != nil {
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

func TestMissingSetLockedDoesNotUnlock(t *testing.T) {
	runner := &recordingRunner{calls: make(chan string, 1)}
	p := NewLockDevicePlugin(config.LockDeviceConfig{}, nil, log.Nop(), sysexec.New(runner))
	dev := &captureSender{}
	if err := p.Handle(context.Background(), dev, lockPacket(t, "kdeconnect.lock.request", struct{}{})); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-runner.calls:
		t.Fatalf("malformed request ran %q", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRemoteUnlockRequiresExplicitOptIn(t *testing.T) {
	runner := &recordingRunner{calls: make(chan string, 2)}
	p := NewLockDevicePlugin(config.LockDeviceConfig{}, nil, log.Nop(), sysexec.New(runner))
	dev := testutil.NewFakeSender("device")
	locked := true
	if err := p.Handle(context.Background(), dev, lockPacket(t, "kdeconnect.lock.request", inboundLockRequest{SetLocked: &locked})); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-runner.calls:
		if got != "loginctl lock-session" {
			t.Fatalf("default policy command = %q, want lock-session", got)
		}
	case <-time.After(time.Second):
		t.Fatal("remote lock did not run")
	}
	unlocked := false
	if err := p.Handle(context.Background(), dev, lockPacket(t, "kdeconnect.lock.request", inboundLockRequest{SetLocked: &unlocked})); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-runner.calls:
		t.Fatalf("default policy allowed %q", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSetLockedActionPublishesState(t *testing.T) {
	runner := &recordingRunner{calls: make(chan string, 1), locked: true}
	p := NewLockDevicePlugin(config.LockDeviceConfig{}, nil, log.Nop(), sysexec.New(runner))
	dev := testutil.NewFakeSender("device")
	locked := true
	if err := p.Handle(context.Background(), dev, lockPacket(t, "kdeconnect.lock.request", inboundLockRequest{SetLocked: &locked})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.calls:
	case <-time.After(time.Second):
		t.Fatal("lock action did not run")
	}
	pkt := dev.WaitFor("kdeconnect.lock", time.Second)
	if pkt == nil {
		t.Fatal("lock action did not publish state")
	}
	var body map[string]any
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["isLocked"] != true {
		t.Fatalf("reported state = %v, want true after locking", body["isLocked"])
	}
}

func TestStateRequestRepliesWithExplicitUnlockedState(t *testing.T) {
	p := NewLockDevicePlugin(config.LockDeviceConfig{}, nil, log.Nop(), sysexec.New(&recordingRunner{}))
	dev := testutil.NewFakeSender("device")
	requested := true
	if err := p.Handle(context.Background(), dev, lockPacket(t, "kdeconnect.lock.request", inboundLockRequest{RequestLocked: &requested})); err != nil {
		t.Fatal(err)
	}
	pkt := dev.WaitFor("kdeconnect.lock", time.Second)
	if pkt == nil {
		t.Fatal("state request received no reply")
	}
	var body map[string]any
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		t.Fatal(err)
	}
	if value, ok := body["isLocked"]; !ok || value != false {
		t.Fatalf("state reply = %s, want isLocked:false", pkt.Body)
	}
}
