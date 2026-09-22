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
