package mpris

import (
	"testing"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

type countingSender struct {
	testSender
	sends int
}

func (s *countingSender) Send(_ *protocol.Packet) error {
	s.sends++
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

	if sender.sends != 0 {
		t.Fatalf("sent %d requests with zero subscribers", sender.sends)
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

	if sender.sends != 1 {
		t.Fatalf("sent %d requests while watched, want 1", sender.sends)
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
	sender.sends = 0
	p.pollRemoteStates()

	if sender.sends != 0 {
		t.Fatalf("sent %d requests after unsubscribe", sender.sends)
	}
}
