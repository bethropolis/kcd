package mousepad

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/sysexec"
)

type inputRunner struct{ calls chan string }

func (r *inputRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls <- name + " " + args[0]
	return nil, nil
}
func (*inputRunner) Output(context.Context, string, ...string) ([]byte, error) { return nil, nil }
func (*inputRunner) RunInput(context.Context, string, string, ...string) ([]byte, error) {
	return nil, nil
}
func (*inputRunner) LookPath(string) (string, error) { return "", errors.New("unused") }

func TestRunCmdUsesInjectedRunner(t *testing.T) {
	runner := &inputRunner{calls: make(chan string, 1)}
	p := NewMousepadPlugin(config.MousepadConfig{Backend: "xdotool"}, log.Nop(), sysexec.New(runner))
	t.Cleanup(func() { _ = p.Close() })
	p.runCmd("xdotool", "click", "1")
	select {
	case got := <-runner.calls:
		if got != "xdotool click" {
			t.Fatalf("call = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("injected runner did not receive command")
	}
}
