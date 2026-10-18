package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
)

// A bare `kcd watch` used to subscribe to every event, delivering SMS bodies
// and arming the phone. SMS has to be named.
func TestUnfilteredWatchExcludesSMS(t *testing.T) {
	bus := events.NewBus(log.Nop())
	conn, rd := startWatchStream(t, bus, nil)
	waitForSubscriber(t, bus, events.TypeBatteryUpdate)

	bus.Publish(events.TypeSMSIncoming, "dev1", map[string]any{"body": "Yes", "sender": "707"})
	bus.Publish(events.TypeBatteryUpdate, "dev1", map[string]any{"charge": 42})

	got := drainEvents(conn, rd, 400*time.Millisecond)
	if _, ok := got[events.TypeSMSIncoming]; ok {
		t.Error("bare watch received sms.incoming")
	}
	if _, ok := got[events.TypeBatteryUpdate]; !ok {
		t.Errorf("bare watch dropped battery.update; got %v", eventKeys(got))
	}
}

func TestWatchDeliversSMSWhenNamed(t *testing.T) {
	bus := events.NewBus(log.Nop())
	conn, rd := startWatchStream(t, bus, []string{"sms.incoming"})
	waitForSubscriber(t, bus, events.TypeSMSIncoming)

	bus.Publish(events.TypeSMSIncoming, "dev1", map[string]any{"body": "Yes", "sender": "707"})

	got := drainEvents(conn, rd, time.Second)
	if _, ok := got[events.TypeSMSIncoming]; !ok {
		t.Errorf("-e sms.incoming received nothing; got %v", eventKeys(got))
	}
}

func TestNamedFilterStillDeliversItsOwnTypes(t *testing.T) {
	bus := events.NewBus(log.Nop())
	conn, rd := startWatchStream(t, bus, []string{"battery.update"})
	waitForSubscriber(t, bus, events.TypeBatteryUpdate)

	bus.Publish(events.TypeBatteryUpdate, "dev1", map[string]any{"charge": 42})
	bus.Publish(events.TypeNotification, "dev1", map[string]any{"title": "hi"})

	got := drainEvents(conn, rd, 400*time.Millisecond)
	if _, ok := got[events.TypeBatteryUpdate]; !ok {
		t.Errorf("dropped the requested type; got %v", eventKeys(got))
	}
	if _, ok := got[events.TypeNotification]; ok {
		t.Error("received a type that was not requested")
	}
}

func startWatchStream(t *testing.T, bus *events.Bus, filters []string) (net.Conn, *bufio.Reader) {
	t.Helper()

	sock := filepath.Join(t.TempDir(), "watch.sock")
	logger := log.NewTest(t)
	h := NewHandler(device.NewRegistry(nil), plugin.NewRegistry(logger), nil, nil, bus, 0)
	srv := NewServer(sock, h, logger)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Listen(ctx) }()
	t.Cleanup(cancel)

	conn := dialWatch(t, sock, filters)
	t.Cleanup(func() { conn.Close() })

	rd := bufio.NewReader(conn)
	// OK line, then the state snapshot.
	_, _ = rd.ReadBytes('\n')
	_, _ = rd.ReadBytes('\n')

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	return conn, rd
}

func dialWatch(t *testing.T, sock string, filters []string) net.Conn {
	t.Helper()
	waitForSocket(t, sock)

	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	req := Request{Command: CmdWatch}
	if len(filters) > 0 {
		data, _ := json.Marshal(WatchPayload{Events: filters})
		req.Payload = data
	}
	line, _ := json.Marshal(req)
	if _, err := conn.Write(append(line, '\n')); err != nil {
		t.Fatalf("write request: %v", err)
	}
	return conn
}

// drainEvents collects events until the stream is quiet for settle. Each read
// gets its own short deadline so a missing event ends the test instead of
// hanging it.
func drainEvents(conn net.Conn, rd *bufio.Reader, settle time.Duration) map[events.EventType]events.Event {
	got := make(map[events.EventType]events.Event)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(settle))
		line, err := rd.ReadBytes('\n')
		if err != nil {
			return got
		}
		var ev events.Event
		if json.Unmarshal(line, &ev) == nil {
			got[ev.Type] = ev
		}
	}
}

func eventKeys(m map[events.EventType]events.Event) []events.EventType {
	out := make([]events.EventType, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// handleWatch subscribes only after the snapshot is written, so publishing
// straight after the read races it.
func waitForSubscriber(t *testing.T, bus *events.Bus, typ events.EventType) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bus.HasSubscribers(typ) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no subscriber for %q within the deadline", typ)
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := (&net.Dialer{}).DialContext(context.Background(), "unix", path); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s never appeared", path)
}
