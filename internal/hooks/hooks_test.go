package hooks

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/sysexec"
)

type recordingRunner struct {
	mu    sync.Mutex
	calls []string
	args  [][]string
	env   [][]string
	done  chan struct{}
}

func (r *recordingRunner) Run(context.Context, string, ...string) ([]byte, error)    { return nil, nil }
func (r *recordingRunner) Output(context.Context, string, ...string) ([]byte, error) { return nil, nil }
func (r *recordingRunner) LookPath(string) (string, error)                           { return "/bin/true", nil }
func (r *recordingRunner) RunInput(_ context.Context, input, name string, args ...string) ([]byte, error) {
	return r.record(input, name, nil, args...)
}

func (r *recordingRunner) RunInputEnv(_ context.Context, input, name string, env []string, args ...string) ([]byte, error) {
	return r.record(input, name, env, args...)
}

func (r *recordingRunner) record(input, name string, env []string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, input)
	r.args = append(r.args, append([]string{name}, args...))
	r.env = append(r.env, append([]string(nil), env...))
	r.mu.Unlock()
	select {
	case r.done <- struct{}{}:
	default:
	}
	return nil, nil
}

func TestRunnerRunsHookWithEventInputAndPlaceholders(t *testing.T) {
	bus := events.NewBus(log.Nop())
	runner := &recordingRunner{done: make(chan struct{}, 1)}
	hook := config.HookConfig{Command: []string{"notify", "{event.type}", "{device.id}", "{payload.charge}"}}
	manager := New(map[string]config.HookConfig{"battery.threshold": hook}, bus, log.Nop())
	manager.exec = sysexec.New(runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)

	bus.Publish(events.TypeBatteryThreshold, "dev1", map[string]any{"charge": 12})
	select {
	case <-runner.done:
	case <-time.After(time.Second):
		t.Fatal("hook did not run")
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if got := runner.args[0]; strings.Join(got, " ") != "notify battery.threshold dev1 12" {
		t.Fatalf("argv = %#v", got)
	}
	if got := strings.Join(runner.env[0], " "); got != "KCD_EVENT_TYPE=battery.threshold KCD_DEVICE_ID=dev1" {
		t.Fatalf("env = %q", got)
	}
	var event events.Event
	if err := json.Unmarshal([]byte(runner.calls[0]), &event); err != nil {
		t.Fatal(err)
	}
	if event.DeviceID != "dev1" {
		t.Fatalf("event device = %q", event.DeviceID)
	}
}

func TestRunnerFiltersDevicesAndDebounces(t *testing.T) {
	bus := events.NewBus(log.Nop())
	runner := &recordingRunner{done: make(chan struct{}, 2)}
	manager := New(map[string]config.HookConfig{"device.connected": {
		Command:  []string{"hook"},
		Devices:  []string{"allowed"},
		Debounce: config.FlexDuration(time.Minute),
	}}, bus, log.Nop())
	manager.exec = sysexec.New(runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)

	bus.Publish(events.TypeDeviceConnected, "blocked", nil)
	bus.Publish(events.TypeDeviceConnected, "allowed", nil)
	bus.Publish(events.TypeDeviceConnected, "allowed", nil)
	select {
	case <-runner.done:
	case <-time.After(time.Second):
		t.Fatal("allowed hook did not run")
	}
	select {
	case <-runner.done:
		t.Fatal("debounced hook ran twice")
	default:
	}
}
