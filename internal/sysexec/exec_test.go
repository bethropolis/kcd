package sysexec

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/log"
)

type fakeRunner struct {
	runCalls  int
	outCalls  int
	pathCalls int
	asyncDone chan struct{}
}

func (f *fakeRunner) Run(context.Context, string, ...string) ([]byte, error) {
	f.runCalls++
	if f.asyncDone != nil {
		close(f.asyncDone)
	}
	return []byte("combined"), errors.New("failed")
}
func (f *fakeRunner) Output(context.Context, string, ...string) ([]byte, error) {
	f.outCalls++
	return []byte("stdout"), nil
}
func (f *fakeRunner) RunInput(ctx context.Context, input, name string, args ...string) ([]byte, error) {
	return f.Run(ctx, name, args...)
}
func (f *fakeRunner) LookPath(name string) (string, error) {
	f.pathCalls++
	return "/bin/" + name, nil
}

func TestExecDelegatesRunAndOutput(t *testing.T) {
	runner := &fakeRunner{}
	e := New(runner)
	out, err := e.Run(context.Background(), "cmd")
	if err == nil || string(out) != "combined" {
		t.Fatalf("Run = %q, %v", out, err)
	}
	out, err = e.Output(context.Background(), "cmd")
	if err != nil || string(out) != "stdout" {
		t.Fatalf("Output = %q, %v", out, err)
	}
}

func TestLookPathCachesSuccessfulResult(t *testing.T) {
	runner := &fakeRunner{}
	e := New(runner)
	for range 2 {
		path, err := e.LookPath("tool")
		if err != nil || path != "/bin/tool" {
			t.Fatalf("LookPath = %q, %v", path, err)
		}
	}
	if runner.pathCalls != 1 {
		t.Fatalf("LookPath calls = %d, want 1", runner.pathCalls)
	}
}

func TestRunAsyncReturnsAndRuns(t *testing.T) {
	runner := &fakeRunner{asyncDone: make(chan struct{})}
	start := time.Now()
	New(runner).RunAsync(log.Nop(), "tool")
	if time.Since(start) > time.Second {
		t.Fatal("RunAsync blocked")
	}
	select {
	case <-runner.asyncDone:
	case <-time.After(time.Second):
		t.Fatal("async command did not run")
	}
}
