package daemon

import (
	"context"
	"crypto/tls"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
)

// parkedOpts returns reconnect options whose timer never fires during the
// test, so any dial must come from an explicit sighting poke.
func parkedOpts() *config.Config {
	opts := config.Defaults()
	opts.Reconnect.SightingDriven = true
	opts.Reconnect.InitialBackoff = "1h"
	opts.Reconnect.MaxBackoff = "5m"
	opts.Reconnect.FallbackMax = "1h"
	opts.Reconnect.StaleAfter = "24h"
	return opts
}

func parkedDevice(t *testing.T, id string) *device.Device {
	t.Helper()
	dev := device.NewDevice(id, "Phone", "phone", log.Nop())
	dev.SetState(device.StatePaired)
	dev.SetLastSeen(time.Now())
	if !dev.TryReconnect() {
		t.Fatal("TryReconnect must succeed for a fresh device")
	}
	return dev
}

// acceptCounter listens on loopback and counts inbound dials, closing each
// immediately so the dial side fails fast.
type acceptCounter struct {
	ln    net.Listener
	count atomic.Int32
}

func newAcceptCounter(t *testing.T) *acceptCounter {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ac := &acceptCounter{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			ac.count.Add(1)
			c.Close()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ac
}

func (ac *acceptCounter) port() int {
	return ac.ln.Addr().(*net.TCPAddr).Port
}

func waitForReconnectExit(t *testing.T, dev *device.Device, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for dev.Reconnecting() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if dev.Reconnecting() {
		t.Fatalf("reconnect loop still running after %v", timeout)
	}
}

// A device silent past the stale horizon must exit without dialling:
// zero timers for pairs that will never return.
func TestReconnectStaleHorizonGivesUp(t *testing.T) {
	ac := newAcceptCounter(t)
	dev := parkedDevice(t, "stale-1")
	dev.SetLastSeen(time.Now().Add(-25 * time.Hour))
	dev.SetLastPort(ac.port())

	opts := parkedOpts()
	identity, err := newTestIdentity()
	if err != nil {
		t.Fatal(err)
	}
	go reconnectWithBackoff(context.Background(), dev, net.ParseIP("127.0.0.1"), identity,
		&tls.Config{InsecureSkipVerify: true}, device.NewRegistry(nil),
		plugin.NewRegistry(log.Nop()), "local", log.Nop(), opts)

	waitForReconnectExit(t, dev, 5*time.Second)
	if n := ac.count.Load(); n != 0 {
		t.Fatalf("stale device dialled %d times, want 0", n)
	}
}

// A sighting poke must dial immediately even with a 1h fallback timer,
// and a burst of pokes must coalesce to a single dial (min-gap guard).
func TestReconnectWakesOnSighting(t *testing.T) {
	ac := newAcceptCounter(t)
	dev := parkedDevice(t, "roam-1")
	dev.SetLastPort(ac.port())

	opts := parkedOpts()
	identity, err := newTestIdentity()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go reconnectWithBackoff(ctx, dev, net.ParseIP("127.0.0.1"), identity,
		&tls.Config{InsecureSkipVerify: true}, device.NewRegistry(nil),
		plugin.NewRegistry(log.Nop()), "local", log.Nop(), opts)

	time.Sleep(100 * time.Millisecond) // let the loop park on the timer
	dev.PokeReconnect()
	dev.PokeReconnect()
	dev.PokeReconnect()

	deadline := time.Now().Add(5 * time.Second)
	for ac.count.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if ac.count.Load() == 0 {
		t.Fatal("sighting poke produced no dial within 5s")
	}
	time.Sleep(400 * time.Millisecond)
	if n := ac.count.Load(); n != 1 {
		t.Fatalf("sighting burst produced %d dials, want exactly 1", n)
	}
}

// Unpairing while parked must exit the loop promptly (no goroutine leak).
func TestReconnectUnpairExitsParkedLoop(t *testing.T) {
	dev := parkedDevice(t, "gone-1")

	opts := parkedOpts()
	identity, err := newTestIdentity()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go reconnectWithBackoff(ctx, dev, net.ParseIP("192.0.2.1"), identity,
		&tls.Config{InsecureSkipVerify: true}, device.NewRegistry(nil),
		plugin.NewRegistry(log.Nop()), "local", log.Nop(), opts)

	time.Sleep(100 * time.Millisecond) // let the loop park
	dev.SetState(device.StateUnpaired) // pokes the wake channel

	waitForReconnectExit(t, dev, 5*time.Second)
}

// Legacy mode (sighting_driven=false) keeps pure-timer dials capped at
// max_backoff: the timer path must still fire without any sighting.
func TestReconnectLegacyTimerStillDials(t *testing.T) {
	ac := newAcceptCounter(t)
	dev := parkedDevice(t, "legacy-1")
	dev.SetLastPort(ac.port())
	dev.SetLastSeen(time.Now())

	opts := config.Defaults()
	opts.Reconnect.SightingDriven = false
	opts.Reconnect.InitialBackoff = "20ms"
	opts.Reconnect.MaxBackoff = "20ms"
	opts.Reconnect.FlapThreshold = "15s"

	identity, err := newTestIdentity()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go reconnectWithBackoff(ctx, dev, net.ParseIP("127.0.0.1"), identity,
		&tls.Config{InsecureSkipVerify: true}, device.NewRegistry(nil),
		plugin.NewRegistry(log.Nop()), "local", log.Nop(), opts)

	deadline := time.Now().Add(5 * time.Second)
	for ac.count.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if ac.count.Load() == 0 {
		t.Fatal("legacy timer produced no dial within 5s")
	}
}
