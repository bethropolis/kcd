package main

import (
	"context"
	"encoding/json"
	"flag"
	"path/filepath"
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

// convStub records the raw payload of each conversation request.
type convStub struct {
	cl       *client.Client
	mu       sync.Mutex
	payloads []json.RawMessage
}

func startConvStub(t *testing.T) *convStub {
	t.Helper()

	sock := filepath.Join(t.TempDir(), "sms.sock")
	logger := log.NewTest(t)
	stub := &convStub{}

	h := ipc.NewHandler(device.NewRegistry(nil), plugin.NewRegistry(logger), nil, nil, nil, 0)
	h.Register(ipc.CmdSmsRequestConv, func(req ipc.Request) ipc.Response {
		stub.mu.Lock()
		stub.payloads = append(stub.payloads, append(json.RawMessage(nil), req.Payload...))
		stub.mu.Unlock()
		return ipc.Response{OK: true}
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = ipc.NewServer(sock, h, logger).Listen(ctx) }()
	time.Sleep(100 * time.Millisecond)

	stub.cl = &client.Client{SocketPath: sock, Timeout: 2 * time.Second}
	return stub
}

func smsConvContext(t *testing.T, args ...string) *cli.Context {
	t.Helper()
	set := flag.NewFlagSet("conversation", flag.ContinueOnError)
	set.Int64("before", 0, "")
	set.Int64("limit", 0, "")
	if err := set.Parse(args); err != nil {
		t.Fatal(err)
	}
	return cli.NewContext(cli.NewApp(), set, nil)
}

func (s *convStub) last(t *testing.T) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.payloads) != 1 {
		t.Fatalf("got %d requests, want 1", len(s.payloads))
	}
	var m map[string]any
	if err := json.Unmarshal(s.payloads[0], &m); err != nil {
		t.Fatalf("payload is not JSON: %q", s.payloads[0])
	}
	return m
}

// Without flags the range fields must be absent from the packet, not zero:
// the phone reads an explicit 0 as "older than the epoch" and returns
// nothing instead of the whole thread.
func TestSmsConversationOmitsRangeByDefault(t *testing.T) {
	stub := startConvStub(t)
	captureStdout(t, func() {
		if err := runSmsConversation(smsConvContext(t, "phone1", "42"), stub.cl); err != nil {
			t.Fatalf("runSmsConversation: %v", err)
		}
	})
	got := stub.last(t)
	if got["deviceId"] != "phone1" || got["threadID"] != float64(42) {
		t.Errorf("identity fields wrong: %v", got)
	}
	if _, ok := got["rangeStartTimestamp"]; ok {
		t.Errorf("rangeStartTimestamp present without --before: %v", got)
	}
	if _, ok := got["numberToRequest"]; ok {
		t.Errorf("numberToRequest present without --limit: %v", got)
	}
}

func TestSmsConversationRangeFlags(t *testing.T) {
	stub := startConvStub(t)
	captureStdout(t, func() {
		err := runSmsConversation(smsConvContext(t, "--before", "1791666622866", "--limit", "25", "phone1", "42"), stub.cl)
		if err != nil {
			t.Fatalf("runSmsConversation: %v", err)
		}
	})
	got := stub.last(t)
	if got["rangeStartTimestamp"] != float64(1791666622866) {
		t.Errorf("rangeStartTimestamp = %v, want 1791666622866", got["rangeStartTimestamp"])
	}
	if got["numberToRequest"] != float64(25) {
		t.Errorf("numberToRequest = %v, want 25", got["numberToRequest"])
	}
}

func TestSmsConversationRejectsBadInput(t *testing.T) {
	stub := startConvStub(t)
	for _, args := range [][]string{
		{"phone1", "nope"},
		{"--limit", "-1", "phone1", "42"},
		{"--before", "-1", "phone1", "42"},
		{"phone1"},
	} {
		stub.mu.Lock()
		stub.payloads = nil
		stub.mu.Unlock()
		if err := runSmsConversation(smsConvContext(t, args...), stub.cl); err == nil {
			t.Errorf("args %v: expected an error, got nil", args)
		}
		stub.mu.Lock()
		n := len(stub.payloads)
		stub.mu.Unlock()
		if n != 0 {
			t.Errorf("args %v: rejected input still sent %d requests", args, n)
		}
	}
}
