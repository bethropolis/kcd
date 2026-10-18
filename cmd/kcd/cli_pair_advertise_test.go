package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/pkg/client"
	"github.com/urfave/cli/v2"
)

// pairStub records the IPC commands that reach the daemon, so a test can assert
// that advertise-only never asks for an acceptance.
type pairStub struct {
	cl   *client.Client
	mu   sync.Mutex
	seen []string
}

func (s *pairStub) count(cmd string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.seen {
		if c == cmd {
			n++
		}
	}
	return n
}

func (s *pairStub) saw(cmd string) bool { return s.count(cmd) > 0 }

func startPairStub(t *testing.T) *pairStub {
	t.Helper()

	sock := filepath.Join(t.TempDir(), "pair.sock")
	logger := log.NewTest(t)
	stub := &pairStub{}

	h := ipc.NewHandler(device.NewRegistry(nil), plugin.NewRegistry(logger), nil, nil, nil, 0)
	for _, name := range []string{ipc.CmdBroadcastStart, ipc.CmdBroadcastStop, ipc.CmdPair, ipc.CmdUnpair} {
		cmd := name
		h.Register(cmd, func(ipc.Request) ipc.Response {
			stub.mu.Lock()
			stub.seen = append(stub.seen, cmd)
			stub.mu.Unlock()
			return ipc.Response{OK: true}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = ipc.NewServer(sock, h, logger).Listen(ctx) }()
	time.Sleep(100 * time.Millisecond)

	stub.cl = &client.Client{SocketPath: sock, Timeout: 2 * time.Second}
	return stub
}

func advertiseContext(args ...string) *cli.Context {
	set := flag.NewFlagSet("pair", flag.ContinueOnError)
	set.Bool("advertise-only", false, "")
	set.Bool("json", false, "")
	set.Bool("yes", false, "")
	set.Bool("known-only", false, "")
	set.String("expected-fingerprint", "", "")
	if err := set.Parse(args); err != nil {
		panic(err)
	}
	return cli.NewContext(cli.NewApp(), set, nil)
}

// captureStdout swaps os.Stdout for a pipe while f runs. advertiseUntil writes
// straight to os.Stdout, so this is the only way to see its output.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w

	drained := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		drained <- string(b)
	}()

	f()

	os.Stdout = orig
	_ = w.Close()
	out := <-drained
	_ = r.Close()
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The mode's whole guarantee is that it never accepts, so it is asserted
// directly rather than inferred from the absence of a crash.
func TestAdvertiseOnlyNeverAccepts(t *testing.T) {
	stub := startPairStub(t)
	stop := make(chan struct{})
	done := make(chan error, 1)

	captureStdout(t, func() {
		go func() { done <- advertiseUntil(advertiseContext(), stub.cl, stop) }()
		waitFor(t, "broadcast_start", func() bool { return stub.saw(ipc.CmdBroadcastStart) })
		close(stop)
		if err := <-done; err != nil {
			t.Errorf("advertiseUntil: %v", err)
		}
	})

	if got := stub.count(ipc.CmdBroadcastStart); got != 1 {
		t.Errorf("broadcast_start sent %d times, want 1", got)
	}
	// The pairing window has to close even when the run is cut short, or the
	// daemon is left advertising to the network.
	if got := stub.count(ipc.CmdBroadcastStop); got != 1 {
		t.Errorf("broadcast_stop sent %d times, want 1", got)
	}
	if got := stub.count(ipc.CmdPair); got != 0 {
		t.Errorf("advertise-only called %s %d times; it must accept nothing", ipc.CmdPair, got)
	}
	if got := stub.count(ipc.CmdUnpair); got != 0 {
		t.Errorf("advertise-only called %s %d times; it rejects nothing either", ipc.CmdUnpair, got)
	}
}

// A supervising client needs to know when the daemon is actually discoverable,
// not merely that the process launched.
func TestAdvertiseOnlyJSONReportsBothTransitions(t *testing.T) {
	stub := startPairStub(t)
	stop := make(chan struct{})

	out := captureStdout(t, func() {
		done := make(chan error, 1)
		go func() { done <- advertiseUntil(advertiseContext("-json"), stub.cl, stop) }()
		waitFor(t, "broadcast_start", func() bool { return stub.saw(ipc.CmdBroadcastStart) })
		close(stop)
		if err := <-done; err != nil {
			t.Errorf("advertiseUntil: %v", err)
		}
	})

	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("output is not NDJSON: %q", line)
		}
		lines = append(lines, m)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), out)
	}
	if lines[0]["advertising"] != true {
		t.Errorf("first line = %v, want advertising:true", lines[0])
	}
	if lines[1]["advertising"] != false {
		t.Errorf("second line = %v, want advertising:false", lines[1])
	}
}

// Contradictory flags are refused, not silently dropped: the caller is a
// program relying on "never accepts".
func TestCheckAdvertiseOnly(t *testing.T) {
	if err := checkAdvertiseOnly(advertiseContext()); err != nil {
		t.Errorf("plain --advertise-only should be accepted: %v", err)
	}
	if err := checkAdvertiseOnly(advertiseContext("-yes")); err == nil {
		t.Error("--advertise-only with --yes should be refused")
	}
	if err := checkAdvertiseOnly(advertiseContext("-json")); err != nil {
		t.Errorf("--advertise-only with --json should be accepted: %v", err)
	}
}
