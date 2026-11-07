package mousepad

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/sysexec"
)

type streamRecordingRunner struct {
	mu     sync.Mutex
	stream *safeBuffer
	runs   []string
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) Close() error {
	return nil
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (r *streamRecordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.runs = append(r.runs, name+" "+strings.Join(args, " "))
	r.mu.Unlock()
	return nil, nil
}

func (r *streamRecordingRunner) RunInput(context.Context, string, string, ...string) ([]byte, error) {
	return nil, nil
}

func (r *streamRecordingRunner) Output(context.Context, string, ...string) ([]byte, error) {
	return nil, nil
}

func (r *streamRecordingRunner) LookPath(string) (string, error) {
	return "", nil
}

func (r *streamRecordingRunner) StartStream(_ context.Context, name string, args ...string) (io.WriteCloser, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stream = &safeBuffer{}
	return r.stream, nil
}

func TestXdotoolStreamingAvoidsForkExec(t *testing.T) {
	runner := &streamRecordingRunner{}
	p := NewMousepadPlugin(config.MousepadConfig{Backend: "xdotool"}, log.Nop(), sysexec.New(runner))
	p.isWayland = false
	t.Cleanup(func() { _ = p.Close() })

	// Send multiple pointer moves
	for i := 0; i < 5; i++ {
		payload, _ := json.Marshal(MousepadBody{Dx: 2, Dy: 3})
		_ = p.Handle(context.Background(), nil, &protocol.Packet{Body: payload})
	}

	// Wait briefly for worker to flush
	time.Sleep(50 * time.Millisecond)

	runner.mu.Lock()
	runCount := len(runner.runs)
	var streamContent string
	if runner.stream != nil {
		streamContent = runner.stream.String()
	}
	runner.mu.Unlock()

	if runCount != 0 {
		t.Fatalf("expected 0 fork+exec Run calls, got %d: %v", runCount, runner.runs)
	}
	if !strings.Contains(streamContent, "mousemove_relative --") {
		t.Fatalf("expected stream to contain mousemove_relative commands, got %q", streamContent)
	}
}
