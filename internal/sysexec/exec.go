// Package sysexec runs desktop tools behind an injectable process boundary.
package sysexec

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/log"
)

// Runner is the process boundary used by Exec and replaceable in tests.
type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
	RunInput(context.Context, string, string, ...string) ([]byte, error)
	Output(context.Context, string, ...string) ([]byte, error)
	LookPath(string) (string, error)
}

// OSRunner executes commands using the host process APIs.
type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return newCommand(ctx, name, args...).CombinedOutput()
}

func (OSRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return newCommand(ctx, name, args...).Output()
}

func (OSRunner) RunInput(ctx context.Context, input, name string, args ...string) ([]byte, error) {
	cmd := newCommand(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	return cmd.CombinedOutput()
}

func (OSRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

// Exec runs commands through a Runner and caches successful executable lookups.
type Exec struct {
	runner Runner
	mu     sync.Mutex
	paths  map[string]string
}

const (
	// asyncTimeout allows desktop services to start while bounding best-effort commands.
	asyncTimeout = 10 * time.Second
	// commandWaitDelay bounds time waiting for descendants that inherit output pipes.
	commandWaitDelay = time.Second
)

func newCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = commandWaitDelay
	return cmd
}

// New creates an execution facade backed by runner.
func New(runner Runner) *Exec {
	if runner == nil {
		runner = OSRunner{}
	}
	return &Exec{runner: runner, paths: make(map[string]string)}
}

func (e *Exec) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return e.runner.Run(ctx, name, args...)
}

func (e *Exec) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return e.runner.Output(ctx, name, args...)
}

func (e *Exec) RunInput(ctx context.Context, input, name string, args ...string) ([]byte, error) {
	return e.runner.RunInput(ctx, input, name, args...)
}

func (e *Exec) LookPath(name string) (string, error) {
	e.mu.Lock()
	if path, ok := e.paths[name]; ok {
		e.mu.Unlock()
		return path, nil
	}
	e.mu.Unlock()
	path, err := e.runner.LookPath(name)
	if err == nil {
		e.mu.Lock()
		e.paths[name] = path
		e.mu.Unlock()
	}
	return path, err
}

// RunAsync executes a command in a goroutine with a bounded lifetime.
func (e *Exec) RunAsync(logger log.Logger, name string, args ...string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), asyncTimeout)
		defer cancel()
		if _, err := e.Run(ctx, name, args...); err != nil {
			logger.Warn("subprocess failed", log.String("cmd", name), log.Error(err))
		}
	}()
}

var defaultExec = New(OSRunner{})

func Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return defaultExec.Run(ctx, name, args...)
}
func Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return defaultExec.Output(ctx, name, args...)
}
func LookPath(name string) (string, error) { return defaultExec.LookPath(name) }
func RunAsync(logger log.Logger, name string, args ...string) {
	defaultExec.RunAsync(logger, name, args...)
}
