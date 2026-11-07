package runcommand

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// outputSender records every packet a run emits so ordering and payload shape
// can be asserted. The execution goroutine calls Send concurrently, so the
// recording is mutex-guarded.
type outputSender struct {
	id   string
	mu   sync.Mutex
	sent []*protocol.Packet
}

func (s *outputSender) ID() string                   { return s.id }
func (s *outputSender) Name() string                 { return "Test" }
func (s *outputSender) SetName(string)               {}
func (s *outputSender) State() device.PairingState   { return device.StatePaired }
func (s *outputSender) SetState(device.PairingState) {}
func (s *outputSender) IsConnected() bool            { return true }
func (s *outputSender) RemoteIP() net.IP             { return nil }
func (s *outputSender) PeerCert() *x509.Certificate  { return nil }
func (s *outputSender) HasCapability(string) bool    { return true }
func (s *outputSender) UpdateBattery(int, bool)      {}
func (s *outputSender) GetBattery() (int, bool)      { return 0, false }

func (s *outputSender) Send(p *protocol.Packet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, p)
	return nil
}

func (s *outputSender) packets() []*protocol.Packet {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*protocol.Packet(nil), s.sent...)
}

func (s *outputSender) outputs() []*protocol.Packet {
	var out []*protocol.Packet
	for _, p := range s.packets() {
		if p.Type == PacketTypeOutput {
			out = append(out, p)
		}
	}
	return out
}

// body decodes a captured packet's body into a generic map.
func body(t *testing.T, p *protocol.Packet) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(p.Body, &m); err != nil {
		t.Fatalf("decode %s body: %v", p.Type, err)
	}
	return m
}

// runKey sends a command request for key and waits for the plugin's background
// goroutines to finish.
func runKey(t *testing.T, p *RunCommandPlugin, dev *outputSender, key string) {
	t.Helper()
	pkt := &protocol.Packet{
		Type: "kdeconnect.runcommand.request",
		Body: json.RawMessage(`{"key":"` + key + `"}`),
	}
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	p.wg.Wait()
}

func newTestPlugin(commands map[string]string) *RunCommandPlugin {
	return NewRunCommandPlugin(commands, nil, nil, log.Nop())
}

func newTestPluginWithBus(commands map[string]string, bus *events.Bus) *RunCommandPlugin {
	return NewRunCommandPlugin(commands, nil, bus, log.Nop())
}

// The phone keys its output rows off the id registered by commandStarted, and
// a finished packet for an unknown id only flips its spinner. The three packet
// types must therefore arrive started -> output -> finished, all sharing one
// id that fits a Java int.
func TestOutputPacketOrdering(t *testing.T) {
	p := newTestPlugin(map[string]string{"hi": "printf 'hello\\n'"})
	dev := &outputSender{id: "dev1"}

	runKey(t, p, dev, "hi")

	outs := dev.outputs()
	if len(outs) < 2 {
		t.Fatalf("got %d output packets, want started+output+finished", len(outs))
	}

	first := body(t, outs[0])
	if first["commandStarted"] != true {
		t.Errorf("first packet is not commandStarted: %v", first)
	}
	if first["command"] != "hi" {
		t.Errorf("commandStarted command = %v, want %q", first["command"], "hi")
	}

	last := body(t, outs[len(outs)-1])
	if last["commandFinished"] != true {
		t.Errorf("last packet is not commandFinished: %v", last)
	}

	id, ok := first["id"].(float64)
	if !ok {
		t.Fatalf("id is not a number: %T", first["id"])
	}
	if int64(id) > 2147483647 || int64(id) < -2147483648 {
		t.Errorf("id %v does not fit a Java int", id)
	}
	if last["id"] != first["id"] {
		t.Errorf("finished id %v != started id %v", last["id"], first["id"])
	}
	if last["success"] != true {
		t.Errorf("success = %v, want true for a command that worked", last["success"])
	}
	if exitCode, ok := last["exitCode"].(float64); !ok || int(exitCode) != 0 {
		t.Errorf("exitCode = %v, want 0", last["exitCode"])
	}
}

// getStringList returns null for a missing key and the phone iterates both
// lists unguarded, so every output batch must carry both keys even when one
// stream produced nothing.
func TestOutputBatchesAlwaysCarryBothStreams(t *testing.T) {
	p := newTestPlugin(map[string]string{"out": "printf 'only stdout\\n'"})
	dev := &outputSender{id: "dev1"}

	runKey(t, p, dev, "out")

	var batches int
	for _, pkt := range dev.outputs() {
		m := body(t, pkt)
		if m["commandOutput"] != true {
			continue
		}
		batches++
		if _, ok := m["stdout"]; !ok {
			t.Errorf("batch missing stdout key: %v", m)
		}
		if _, ok := m["stderr"]; !ok {
			t.Errorf("batch missing stderr key: %v", m)
		}
	}
	if batches == 0 {
		t.Fatal("no commandOutput packet was sent")
	}
}

func TestOutputStreamsStdoutAndStderrSeparately(t *testing.T) {
	p := newTestPlugin(map[string]string{"mix": "printf 'to out\\n'; printf 'to err\\n' >&2"})
	dev := &outputSender{id: "dev1"}

	runKey(t, p, dev, "mix")

	var stdout, stderr []string
	for _, pkt := range dev.outputs() {
		m := body(t, pkt)
		if m["commandOutput"] != true {
			continue
		}
		for _, v := range m["stdout"].([]any) {
			stdout = append(stdout, v.(string))
		}
		for _, v := range m["stderr"].([]any) {
			stderr = append(stderr, v.(string))
		}
	}

	if !contains(stdout, "to out") {
		t.Errorf("stdout = %v, want it to contain %q", stdout, "to out")
	}
	if !contains(stderr, "to err") {
		t.Errorf("stderr = %v, want it to contain %q", stderr, "to err")
	}
	if contains(stdout, "to err") {
		t.Errorf("stderr line leaked into stdout: %v", stdout)
	}
}

// A command that prints nothing still has to be bracketed, otherwise the
// phone's spinner never clears.
func TestSilentCommandStillBracketsExecution(t *testing.T) {
	p := newTestPlugin(map[string]string{"quiet": "true"})
	dev := &outputSender{id: "dev1"}

	runKey(t, p, dev, "quiet")

	outs := dev.outputs()
	if len(outs) < 2 {
		t.Fatalf("got %d output packets, want started+finished", len(outs))
	}
	if body(t, outs[0])["commandStarted"] != true {
		t.Error("missing commandStarted")
	}
	if body(t, outs[len(outs)-1])["commandFinished"] != true {
		t.Error("missing commandFinished")
	}
}

func TestFailedCommandReportsFailure(t *testing.T) {
	p := newTestPlugin(map[string]string{"bad": "exit 3"})
	dev := &outputSender{id: "dev1"}

	runKey(t, p, dev, "bad")

	outs := dev.outputs()
	if len(outs) == 0 {
		t.Fatal("no output packets")
	}
	last := body(t, outs[len(outs)-1])
	if last["success"] != false {
		t.Errorf("success = %v, want false for a failing command", last["success"])
	}
}

// The line cap is the only bound protecting the phone's unbounded output list.
func TestOutputLineCapTruncates(t *testing.T) {
	p := newTestPlugin(map[string]string{"spam": "seq 1 5000"})
	dev := &outputSender{id: "dev1"}

	runKey(t, p, dev, "spam")

	var lines int
	var sawTruncation bool
	for _, pkt := range dev.outputs() {
		m := body(t, pkt)
		if m["commandOutput"] != true {
			continue
		}
		for _, v := range m["stderr"].([]any) {
			lines++
			if s, ok := v.(string); ok && s != "" && s[len(s)-1:] == "]" {
				sawTruncation = true
			}
		}
	}
	if lines == 0 {
		t.Fatal("no output reached the phone")
	}
	if lines > streamMaxLines+10 {
		t.Errorf("sent %d lines, want the cap of %d to hold", lines, streamMaxLines)
	}
	if !sawTruncation {
		t.Error("truncation was not reported to the phone")
	}
}

// Handle must return before the command finishes, or it stalls the whole
// per-device read loop.
func TestHandleDoesNotBlockOnCommand(t *testing.T) {
	p := newTestPlugin(map[string]string{"slow": "sleep 2; printf done"})
	dev := &outputSender{id: "dev1"}

	pkt := &protocol.Packet{
		Type: "kdeconnect.runcommand.request",
		Body: json.RawMessage(`{"key":"slow"}`),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := p.Handle(context.Background(), dev, pkt); err != nil {
			t.Errorf("Handle: %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Handle blocked on command execution")
	}
	p.wg.Wait()
}

// The phone's stop button sends {"stop": true} with an execution id.
func TestStopRequestCancelsExecution(t *testing.T) {
	p := newTestPlugin(map[string]string{"long": "sleep 30"})
	dev := &outputSender{id: "dev1"}

	pkt := &protocol.Packet{
		Type: "kdeconnect.runcommand.request",
		Body: json.RawMessage(`{"key":"long"}`),
	}
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// Wait for the execution to register before stopping it.
	var id int32
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.RLock()
		for candidate := range p.running["dev1"] {
			id = candidate
		}
		p.mu.RUnlock()
		if id != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if id == 0 {
		p.wg.Wait()
		t.Fatal("execution never registered")
	}

	stopBody, err := json.Marshal(map[string]any{"stop": true, "id": id})
	if err != nil {
		t.Fatalf("marshal stop: %v", err)
	}
	stop := &protocol.Packet{Type: "kdeconnect.runcommand.request", Body: stopBody}
	if err := p.Handle(context.Background(), dev, stop); err != nil {
		t.Fatalf("stop Handle: %v", err)
	}

	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not cancel the execution")
	}

	p.mu.RLock()
	remaining := len(p.running["dev1"])
	p.mu.RUnlock()
	if remaining != 0 {
		t.Errorf("%d executions still registered after stop", remaining)
	}
}

// Android sends {"stop": true} without any execution id.
func TestStopRequestWithoutIDCancelsAll(t *testing.T) {
	p := newTestPlugin(map[string]string{"long1": "sleep 30", "long2": "sleep 30"})
	dev := &outputSender{id: "dev1"}

	if err := p.Handle(context.Background(), dev, &protocol.Packet{
		Type: "kdeconnect.runcommand.request",
		Body: json.RawMessage(`{"key":"long1"}`),
	}); err != nil {
		t.Fatalf("Handle long1: %v", err)
	}
	if err := p.Handle(context.Background(), dev, &protocol.Packet{
		Type: "kdeconnect.runcommand.request",
		Body: json.RawMessage(`{"key":"long2"}`),
	}); err != nil {
		t.Fatalf("Handle long2: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.RLock()
		n := len(p.running["dev1"])
		p.mu.RUnlock()
		if n == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	stop := &protocol.Packet{
		Type: "kdeconnect.runcommand.request",
		Body: json.RawMessage(`{"stop": true}`),
	}
	if err := p.Handle(context.Background(), dev, stop); err != nil {
		t.Fatalf("stop Handle: %v", err)
	}

	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop without id did not cancel executions")
	}

	p.mu.RLock()
	remaining := len(p.running["dev1"])
	p.mu.RUnlock()
	if remaining != 0 {
		t.Errorf("%d executions still registered after stop", remaining)
	}
}

func TestOnDisconnectCancelsRunning(t *testing.T) {
	p := newTestPlugin(map[string]string{"long": "sleep 30"})
	dev := &outputSender{id: "dev1"}

	pkt := &protocol.Packet{
		Type: "kdeconnect.runcommand.request",
		Body: json.RawMessage(`{"key":"long"}`),
	}
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.RLock()
		n := len(p.running["dev1"])
		p.mu.RUnlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	p.OnDisconnect(dev)

	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("disconnect did not cancel the running execution")
	}
}

// The output packet type must be advertised, or the phone will not expect it.
func TestOutputTypeIsAdvertised(t *testing.T) {
	p := newTestPlugin(nil)
	var found bool
	for _, typ := range p.OutgoingTypes() {
		if typ == PacketTypeOutput {
			found = true
		}
	}
	if !found {
		t.Errorf("OutgoingTypes = %v, want it to include %q", p.OutgoingTypes(), PacketTypeOutput)
	}
}

// The notification fallback stays: it is the only channel for users who have
// not enabled the phone's in-app output card.
func TestNotificationFallbackStillSent(t *testing.T) {
	p := newTestPlugin(map[string]string{"hi": "printf 'fallback text\\n'"})
	dev := &outputSender{id: "dev1"}

	runKey(t, p, dev, "hi")

	var notif *protocol.Packet
	for _, pkt := range dev.packets() {
		if pkt.Type == "kdeconnect.notification" {
			notif = pkt
		}
	}
	if notif == nil {
		t.Fatal("no notification packet was sent")
	}
	m := body(t, notif)
	if ticker, _ := m["ticker"].(string); !contains([]string{ticker}, "fallback text") {
		t.Errorf("notification ticker = %q, want the command output", ticker)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// A command that forks a child and waits is the shape of any real workload
// that spawns a subprocess. exec.CommandContext kills only the shell, so the
// orphan keeps the pipe write end open: without closing our read ends on
// cancellation the scanners never see EOF and the plugin's WaitGroup never
// drains. This is the case that hung in CI, where /bin/sh forks where the
// local one happens to exec.
func TestStopCancelsCommandThatForksChildren(t *testing.T) {
	p := newTestPlugin(map[string]string{"fork": "{ sleep 30 & wait; }"})
	dev := &outputSender{id: "dev1"}

	pkt := &protocol.Packet{
		Type: "kdeconnect.runcommand.request",
		Body: json.RawMessage(`{"key":"fork"}`),
	}
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var id int32
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.RLock()
		for candidate := range p.running["dev1"] {
			id = candidate
		}
		p.mu.RUnlock()
		if id != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if id == 0 {
		p.wg.Wait()
		t.Fatal("execution never registered")
	}

	stopBody, err := json.Marshal(map[string]any{"stop": true, "id": id})
	if err != nil {
		t.Fatalf("marshal stop: %v", err)
	}
	if err := p.Handle(context.Background(), dev, &protocol.Packet{
		Type: "kdeconnect.runcommand.request",
		Body: stopBody,
	}); err != nil {
		t.Fatalf("stop Handle: %v", err)
	}

	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("stop did not cancel a forking command")
	}
}

// drainEvents collects published payloads for the given event type.
func drainEvents(t *testing.T, sub *events.Subscriber) []map[string]any {
	t.Helper()
	var out []map[string]any
	for {
		select {
		case ev := <-sub.C:
			payload, ok := ev.Payload.(map[string]any)
			if !ok {
				t.Fatalf("payload is %T, want map[string]any", ev.Payload)
			}
			out = append(out, payload)
		case <-time.After(150 * time.Millisecond):
			return out
		}
	}
}

func statuses(evs []map[string]any) []string {
	got := make([]string, 0, len(evs))
	for _, e := range evs {
		s, _ := e["status"].(string)
		got = append(got, s)
	}
	return got
}

// With a subscriber attached, output must stream as it arrives -- that is the
// whole point of the gated publish.
func TestOutputStream_PublishesBatchesWhenSubscribed(t *testing.T) {
	bus := events.NewBus(log.Nop())
	sub := bus.Subscribe(0, events.TypeRunCommandOutput)
	defer sub.Close()

	p := newTestPluginWithBus(map[string]string{"hello": "echo one; echo two"}, bus)
	dev := &outputSender{id: "dev1"}
	runKey(t, p, dev, "hello")

	evs := drainEvents(t, sub)
	got := statuses(evs)
	if got[0] != "started" {
		t.Errorf("first event = %q, want started", got[0])
	}
	if got[len(got)-1] != "finished" {
		t.Errorf("last event = %q, want finished", got[len(got)-1])
	}
	if len(got) < 3 {
		t.Fatalf("expected at least started/output/finished, got %v", got)
	}
	if !slices.Contains(got, "output") {
		t.Errorf("no output batch was published while subscribed, got %v", got)
	}
	if evs[len(evs)-1]["success"] != true {
		t.Errorf("finished event success = %v, want true", evs[len(evs)-1]["success"])
	}
}

// The batch gate asks HasSubscribers, so what matters is that an unrelated
// filter does not open it. There is no way to observe a *suppressed* publish
// from outside -- the bus drops anything the subscriber did not ask for, which
// is exactly the same observable outcome -- so this pins the gate's input.
func TestRunCommandOutputGate_FollowsSubscribers(t *testing.T) {
	bus := events.NewBus(log.Nop())

	if bus.HasSubscribers(events.TypeRunCommandOutput) {
		t.Error("gate open with no subscribers at all")
	}

	other := bus.Subscribe(0, events.TypeBatteryUpdate)
	defer other.Close()
	if bus.HasSubscribers(events.TypeRunCommandOutput) {
		t.Error("gate opened by a subscriber filtered on another event type")
	}

	watcher := bus.Subscribe(0, events.TypeRunCommandOutput)
	defer watcher.Close()
	if !bus.HasSubscribers(events.TypeRunCommandOutput) {
		t.Error("gate closed while a runcommand.output subscriber is attached")
	}
}
