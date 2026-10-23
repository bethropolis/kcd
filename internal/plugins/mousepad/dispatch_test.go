package mousepad

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/sysexec"
)

type recordRunner struct {
	calls chan string
}

func (r *recordRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls <- name + " " + strings.Join(args, " ")
	return nil, nil
}
func (*recordRunner) Output(context.Context, string, ...string) ([]byte, error) { return nil, nil }
func (*recordRunner) RunInput(context.Context, string, string, ...string) ([]byte, error) {
	return nil, nil
}
func (*recordRunner) LookPath(string) (string, error) { return "", nil }

func TestShortcutsSendModifierAndKeyCombined(t *testing.T) {
	runner := &recordRunner{calls: make(chan string, 10)}
	p := NewMousepadPlugin(config.MousepadConfig{Backend: "xdotool"}, log.Nop(), sysexec.New(runner))
	p.isWayland = false
	t.Cleanup(func() { _ = p.Close() })

	body := MousepadBody{Key: "c", Ctrl: true}
	payload, _ := json.Marshal(body)
	pkt := &protocol.Packet{Body: payload}

	if err := p.Handle(context.Background(), nil, pkt); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	select {
	case call := <-runner.calls:
		if call != "xdotool key ctrl+c" {
			t.Fatalf("expected 'xdotool key ctrl+c', got %q", call)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for shortcut command")
	}

	// Also test Wayland
	p.isWayland = true
	if err := p.Handle(context.Background(), nil, pkt); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	select {
	case call := <-runner.calls:
		if call != "wtype -M ctrl c -m ctrl" {
			t.Fatalf("expected 'wtype -M ctrl c -m ctrl', got %q", call)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Wayland shortcut command")
	}
}

func TestTypingSequencePreservesOrder(t *testing.T) {
	runner := &recordRunner{calls: make(chan string, 50)}
	p := NewMousepadPlugin(config.MousepadConfig{Backend: "xdotool"}, log.Nop(), sysexec.New(runner))
	p.isWayland = false
	t.Cleanup(func() { _ = p.Close() })

	const count = 20
	for i := 0; i < count; i++ {
		ch := string(rune('a' + i))
		payload, _ := json.Marshal(MousepadBody{Key: ch})
		if err := p.Handle(context.Background(), nil, &protocol.Packet{Body: payload}); err != nil {
			t.Fatalf("Handle %d: %v", i, err)
		}
	}

	for i := 0; i < count; i++ {
		want := fmt.Sprintf("xdotool type -- %s", string(rune('a'+i)))
		select {
		case got := <-runner.calls:
			if got != want {
				t.Fatalf("call %d: expected %q, got %q", i, want, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for call %d", i)
		}
	}
}

func TestMotionCoalescingAndFractionalAccumulation(t *testing.T) {
	runner := &recordRunner{calls: make(chan string, 10)}
	p := NewMousepadPlugin(config.MousepadConfig{Backend: "xdotool"}, log.Nop(), sysexec.New(runner))
	p.isWayland = false
	t.Cleanup(func() { _ = p.Close() })

	// Send three sub-pixel movements: 0.4 each -> total 1.2
	for i := 0; i < 3; i++ {
		payload, _ := json.Marshal(MousepadBody{Dx: 0.4, Dy: 0.5})
		if err := p.Handle(context.Background(), nil, &protocol.Packet{Body: payload}); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}

	var totalX, totalY int
	deadline := time.After(time.Second)
	for totalX < 1 || totalY < 1 {
		select {
		case call := <-runner.calls:
			var x, y int
			if _, err := fmt.Sscanf(call, "xdotool mousemove_relative -- %d %d", &x, &y); err == nil {
				totalX += x
				totalY += y
			}
		case <-deadline:
			t.Fatalf("timed out: totalX=%d, totalY=%d", totalX, totalY)
		}
	}

	if totalX != 1 || totalY != 1 {
		t.Fatalf("expected total movement 1 1, got %d %d", totalX, totalY)
	}
}
