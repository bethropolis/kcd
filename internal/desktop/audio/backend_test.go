package audio

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bethropolis/kcd/internal/sysexec"
)

type audioRunner struct{ calls [][]string }

func (r *audioRunner) LookPath(name string) (string, error) {
	if name == "wpctl" {
		return "/usr/bin/wpctl", nil
	}
	return "", errors.New("missing")
}
func (r *audioRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "wpctl" && len(args) > 0 && args[0] == "get-volume" {
		return []byte("Volume: 0.42 [MUTED]"), nil
	}
	return nil, errors.New("unexpected output call")
}
func (r *audioRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return nil, nil
}
func (r *audioRunner) RunInput(context.Context, string, string, ...string) ([]byte, error) {
	return nil, errors.New("unexpected input call")
}

func TestBackendReadAndSet(t *testing.T) {
	runner := &audioRunner{}
	backend := New(sysexec.New(runner))
	volume, muted, err := backend.Read(context.Background())
	if err != nil || volume != 42 || !muted {
		t.Fatalf("Read = %d, %t, %v", volume, muted, err)
	}
	if err := backend.Set(context.Background(), 0, false); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 || !strings.Contains(strings.Join(runner.calls[0], " "), "set-volume @DEFAULT_AUDIO_SINK@ 0%") || !strings.Contains(strings.Join(runner.calls[1], " "), "set-mute @DEFAULT_AUDIO_SINK@ 0") {
		t.Fatalf("commands = %#v", runner.calls)
	}
}
