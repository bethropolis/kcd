package mpris

import (
	"sync/atomic"
	"testing"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// countingSender counts packets atomically: the plugin's real D-Bus
// watcher can discover an actual player on the session bus and broadcast
// from its own goroutine while the test goroutine reads the count.
type countingSender struct {
	testSender
	sends atomic.Int32
}

func (s *countingSender) Send(_ *protocol.Packet) error {
	s.sends.Add(1)
	return nil
}

func playingRemotePlugin(t *testing.T, bus *events.Bus) (*MPRISPlugin, *countingSender) {
	t.Helper()
	p := NewMPRISPlugin(nil, bus, false, config.MPRISConfig{}, log.Nop())
	sender := &countingSender{testSender: testSender{id: "dev1"}}
	p.mu.Lock()
	p.devices["dev1"] = sender
	p.remoteStates["dev1"] = &NowPlaying{Player: "Spotify", IsPlaying: true}
	p.mu.Unlock()
	return p, sender
}

// With nobody listening for mpris.update, the poller must send zero
// requests even while a remote player is playing.
func TestPollRemoteSilentWithoutSubscribers(t *testing.T) {
	bus := events.NewBus(log.Nop())
	p, sender := playingRemotePlugin(t, bus)

	p.pollRemoteStates()

	if sender.sends.Load() != 0 {
		t.Fatalf("sent %d requests with zero subscribers", sender.sends.Load())
	}
}

// Subscribing for mpris.update re-arms the refresh: the next poll emits
// the status request for the playing remote.
func TestPollRemoteRefreshesWhileWatched(t *testing.T) {
	bus := events.NewBus(log.Nop())
	p, sender := playingRemotePlugin(t, bus)

	sub := bus.Subscribe(4, events.TypeMprisUpdate)
	defer sub.Close()

	p.pollRemoteStates()

	if sender.sends.Load() != 1 {
		t.Fatalf("sent %d requests while watched, want 1", sender.sends.Load())
	}
}

// The ticker itself is demand-driven: with zero subscribers no poller
// goroutine exists, the first mpris.update subscribe starts it, and the
// last unsubscribe stops it. The hook fires synchronously inside
// Subscribe/Close, so no waiting is needed.
func TestRemotePollerFollowsSubscribers(t *testing.T) {
	bus := events.NewBus(log.Nop())
	p := NewMPRISPlugin(nil, bus, false, config.MPRISConfig{}, log.Nop())
	if p.watchCancel != nil {
		defer p.watchCancel()
	}

	polling := func() bool {
		p.mu.RLock()
		defer p.mu.RUnlock()
		return p.remotePollCancel != nil
	}

	if polling() {
		t.Fatal("remote poller running with zero subscribers")
	}
	sub := bus.Subscribe(4, events.TypeMprisUpdate)
	if !polling() {
		t.Fatal("remote poller not started on first subscribe")
	}
	sub.Close()
	if polling() {
		t.Fatal("remote poller still running after last unsubscribe")
	}
}

// Unsubscribing silences the poller again — attach/detach cycles must not
// leak refreshes.
func TestPollRemoteSilentAfterUnsubscribe(t *testing.T) {
	bus := events.NewBus(log.Nop())
	p, sender := playingRemotePlugin(t, bus)

	sub := bus.Subscribe(4, events.TypeMprisUpdate)
	p.pollRemoteStates()
	sub.Close()
	sender.sends.Store(0)
	p.pollRemoteStates()

	if sender.sends.Load() != 0 {
		t.Fatalf("sent %d requests after unsubscribe", sender.sends.Load())
	}
}
