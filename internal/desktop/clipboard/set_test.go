package clipboard

import (
	"context"
	"errors"
	"testing"

	"github.com/bethropolis/kcd/internal/sysexec"
)

type clipboardRunner struct {
	input, name string
	args        []string
}

func (r *clipboardRunner) Run(context.Context, string, ...string) ([]byte, error)    { return nil, nil }
func (r *clipboardRunner) Output(context.Context, string, ...string) ([]byte, error) { return nil, nil }
func (r *clipboardRunner) RunInput(_ context.Context, input, name string, args ...string) ([]byte, error) {
	r.input, r.name, r.args = input, name, args
	return nil, nil
}
func (r *clipboardRunner) LookPath(name string) (string, error) {
	if name == "wl-copy" {
		return "/bin/wl-copy", nil
	}
	return "", errors.New("not installed")
}

func TestSetTextUsesWaylandClipboard(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "wayland-1")
	runner := &clipboardRunner{}
	if err := SetText(context.Background(), "clipboard text", sysexec.New(runner)); err != nil {
		t.Fatal(err)
	}
	if runner.name != "wl-copy" || runner.input != "clipboard text" || len(runner.args) != 0 {
		t.Fatalf("invocation = %s %v with %q", runner.name, runner.args, runner.input)
	}
}
