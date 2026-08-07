package mpris

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

type testSender struct {
	id string
}

func (s testSender) ID() string                              { return s.id }
func (s testSender) Name() string                            { return "" }
func (s testSender) SetName(string)                          {}
func (s testSender) State() device.PairingState              { return device.StateUnpaired }
func (s testSender) SetState(device.PairingState)            {}
func (s testSender) Send(*protocol.Packet) error             { return nil }
func (s testSender) IsConnected() bool                       { return true }
func (s testSender) RemoteIP() net.IP                        { return nil }
func (s testSender) PeerCert() *x509.Certificate             { return nil }
func (s testSender) HasCapability(string) bool               { return false }
func (s testSender) UpdateBattery(charge int, charging bool) {}
func (s testSender) GetBattery() (int, bool)                 { return 0, false }

func TestHandleDeduplicatesRemoteMPRISUpdates(t *testing.T) {
	bus := events.NewBus(zap.NewNop())
	sub := bus.Subscribe(4, events.TypeMprisUpdate)
	defer sub.Close()

	plugin := NewMPRISPlugin(nil, bus, false, zap.NewNop())
	if plugin.watchCancel != nil {
		defer plugin.watchCancel()
	}
	dev := testSender{id: "device-1"}
	state := MPRISRequest{
		Player:         "Metrolist",
		Title:          "Sour Grapes",
		Artist:         "LE SSERAFIM",
		Album:          "FEARLESS",
		Pos:            1000,
		IsPlaying:      true,
		Volume:         80,
		PlaybackStatus: "Playing",
	}
	pkt := newMPRISPacket(t, state)

	if err := plugin.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, sub)

	positionOnly := state
	positionOnly.Pos = 2000
	if err := plugin.Handle(context.Background(), dev, newMPRISPacket(t, positionOnly)); err != nil {
		t.Fatal(err)
	}
	expectNoEvent(t, sub)

	got := plugin.RemoteState(dev.ID())
	if got == nil {
		t.Fatal("expected remote state")
	}
	if got.Pos < positionOnly.Pos {
		t.Fatalf("expected position tracker to update to at least %d, got %d", positionOnly.Pos, got.Pos)
	}

	changed := positionOnly
	changed.Title = "Blue Flame"
	if err := plugin.Handle(context.Background(), dev, newMPRISPacket(t, changed)); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, sub)
}

func newMPRISPacket(t *testing.T, body MPRISRequest) *protocol.Packet {
	t.Helper()
	pkt, err := protocol.NewPacket("kdeconnect.mpris", body)
	if err != nil {
		t.Fatal(err)
	}
	return pkt
}

func expectEvent(t *testing.T, sub *events.Subscriber) {
	t.Helper()
	select {
	case <-sub.C:
	case <-time.After(time.Second):
		t.Fatal("expected event")
	}
}

func expectNoEvent(t *testing.T, sub *events.Subscriber) {
	t.Helper()
	select {
	case ev := <-sub.C:
		t.Fatalf("unexpected event: %#v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

type recordingSender struct {
	id      string
	mu      sync.Mutex
	packets []*protocol.Packet
}

func (s *recordingSender) ID() string                   { return s.id }
func (s *recordingSender) Name() string                 { return "" }
func (s *recordingSender) SetName(string)               {}
func (s *recordingSender) State() device.PairingState   { return device.StateUnpaired }
func (s *recordingSender) SetState(device.PairingState) {}
func (s *recordingSender) Send(p *protocol.Packet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.packets = append(s.packets, p)
	return nil
}
func (s *recordingSender) IsConnected() bool                       { return true }
func (s *recordingSender) RemoteIP() net.IP                        { return nil }
func (s *recordingSender) PeerCert() *x509.Certificate             { return nil }
func (s *recordingSender) HasCapability(string) bool               { return false }
func (s *recordingSender) UpdateBattery(charge int, charging bool) {}
func (s *recordingSender) GetBattery() (int, bool)                 { return 0, false }

func (s *recordingSender) sent() []*protocol.Packet {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*protocol.Packet, len(s.packets))
	copy(out, s.packets)
	return out
}

func TestHandleRequestsAlbumArtForKdeconnectURI(t *testing.T) {
	plugin := NewMPRISPlugin(nil, events.NewBus(zap.NewNop()), false, zap.NewNop())
	if plugin.watchCancel != nil {
		defer plugin.watchCancel()
	}
	dev := &recordingSender{id: "device-art"}

	state := MPRISRequest{
		Player:      "Spotify",
		Title:       "Some Track",
		Artist:      "Some Artist",
		IsPlaying:   true,
		AlbumArtUrl: "kdeconnect:/artUri?title=Some+Track&kdeArtHash=12345",
	}
	if err := plugin.Handle(context.Background(), dev, newMPRISPacket(t, state)); err != nil {
		t.Fatal(err)
	}

	// requestAlbumArt runs async — wait for the packet to be sent.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(dev.sent()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	plugin.mu.Lock()
	_, requested := plugin.artRequests["req|device-art|"+state.AlbumArtUrl]
	plugin.mu.Unlock()
	if !requested {
		t.Fatal("expected an album art request to be recorded")
	}

	if len(dev.sent()) == 0 {
		t.Fatal("expected at least one packet sent to the device")
	}
	pkt := dev.sent()[0]
	if pkt.Type != "kdeconnect.mpris.request" {
		t.Fatalf("expected kdeconnect.mpris.request, got %s", pkt.Type)
	}
	var body MPRISRequest
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Player != "Spotify" || body.AlbumArtUrl != state.AlbumArtUrl {
		t.Fatalf("unexpected request body: %+v", body)
	}
}

func TestHandleIgnoresEmptyAlbumArtPayload(t *testing.T) {
	plugin := NewMPRISPlugin(nil, events.NewBus(zap.NewNop()), false, zap.NewNop())
	if plugin.watchCancel != nil {
		defer plugin.watchCancel()
	}
	dev := &recordingSender{id: "device-art-empty"}

	// A transferringAlbumArt packet with no payload must not crash and
	// must not attempt a download (RemoteIP() is nil in the test sender).
	pkt := newMPRISPacket(t, MPRISRequest{
		Player:               "Spotify",
		TransferringAlbumArt: true,
		AlbumArtUrl:          "kdeconnect:/artUri?kdeArtHash=1",
	})
	if err := plugin.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // let any goroutine finish
}

func TestPollRemoteStatesOnlyTargetsKnownPlayers(t *testing.T) {
	plugin := NewMPRISPlugin(nil, events.NewBus(zap.NewNop()), false, zap.NewNop())
	if plugin.watchCancel != nil {
		defer plugin.watchCancel()
	}

	withPlayer := &recordingSender{id: "device-with-player"}
	stoppedPlayer := &recordingSender{id: "device-stopped-player"}
	noPlayer := &recordingSender{id: "device-no-player"}

	// Seed cached remote state for the device with a known active player.
	state := MPRISRequest{Player: "Spotify", Title: "T", Artist: "A", IsPlaying: true}
	if err := plugin.Handle(context.Background(), withPlayer, newMPRISPacket(t, state)); err != nil {
		t.Fatal(err)
	}

	// Seed cached state for a device whose player is stopped/paused — the
	// poller must not keep refreshing it.
	stopped := MPRISRequest{Player: "Namida", Title: "S", Artist: "B", IsPlaying: false}
	if err := plugin.Handle(context.Background(), stoppedPlayer, newMPRISPacket(t, stopped)); err != nil {
		t.Fatal(err)
	}

	plugin.mu.Lock()
	plugin.devices[withPlayer.id] = withPlayer
	plugin.devices[stoppedPlayer.id] = stoppedPlayer
	plugin.devices[noPlayer.id] = noPlayer
	plugin.mu.Unlock()

	// Clear the recorded art-request packets from Handle so we can assert
	// exactly what the poller sends.
	withPlayer.sent()
	stoppedPlayer.sent()

	plugin.pollRemoteStates()

	sent := withPlayer.sent()
	if len(sent) == 0 {
		t.Fatal("expected poller to request state from device with known active player")
	}
	var body MPRISRequest
	if err := json.Unmarshal(sent[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if !body.RequestNowPlaying {
		t.Fatalf("expected requestNowPlaying=true in poll request, got %+v", body)
	}
	if body.Player != "Spotify" {
		t.Fatalf("expected poll request for player Spotify, got %q", body.Player)
	}
	if got := stoppedPlayer.sent(); len(got) != 0 {
		t.Fatalf("expected no poll request to device with stopped player, got %d packets", len(got))
	}
	if got := noPlayer.sent(); len(got) != 0 {
		t.Fatalf("expected no poll request to device without cached player, got %d packets", len(got))
	}
}

func newMPRISRawPacket(t *testing.T, body map[string]interface{}) *protocol.Packet {
	t.Helper()
	pkt, err := protocol.NewPacket("kdeconnect.mpris", body)
	if err != nil {
		t.Fatal(err)
	}
	return pkt
}

func TestHandlePrunesRemovedPlayer(t *testing.T) {
	bus := events.NewBus(zap.NewNop())
	sub := bus.Subscribe(4, events.TypeMprisUpdate)
	defer sub.Close()

	plugin := NewMPRISPlugin(nil, bus, false, zap.NewNop())
	if plugin.watchCancel != nil {
		defer plugin.watchCancel()
	}
	dev := &recordingSender{id: "device-prune"}

	state := MPRISRequest{Player: "Namida", Title: "Otonoke", Artist: "Creepy Nuts", IsPlaying: true}
	if err := plugin.Handle(context.Background(), dev, newMPRISPacket(t, state)); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, sub) // initial state event

	// The phone destroys the session: playerList no longer contains Namida.
	list := newMPRISPacket(t, MPRISRequest{PlayerList: []string{"Spotify"}})
	if err := plugin.Handle(context.Background(), dev, list); err != nil {
		t.Fatal(err)
	}

	if got := plugin.RemoteState(dev.ID()); got != nil {
		t.Fatalf("expected remote state to be pruned, got %+v", got)
	}
	plugin.mu.Lock()
	_, hasTracker := plugin.positionTrackers[dev.ID()]
	_, hasTime := plugin.remoteStateTimes[dev.ID()]
	plugin.mu.Unlock()
	if hasTracker {
		t.Fatal("expected position tracker to be pruned")
	}
	if hasTime {
		t.Fatal("expected remote state time to be pruned")
	}

	// Empty update published so watchers fall back to "no media playing".
	expectEvent(t, sub)
}

func TestHandlePrunesPlayerOnEmptyList(t *testing.T) {
	bus := events.NewBus(zap.NewNop())
	sub := bus.Subscribe(4, events.TypeMprisUpdate)
	defer sub.Close()

	plugin := NewMPRISPlugin(nil, bus, false, zap.NewNop())
	if plugin.watchCancel != nil {
		defer plugin.watchCancel()
	}
	dev := &recordingSender{id: "device-empty-list"}

	state := MPRISRequest{Player: "Namida", Title: "Otonoke", IsPlaying: true}
	if err := plugin.Handle(context.Background(), dev, newMPRISPacket(t, state)); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, sub) // initial state event

	// All sessions destroyed — Android sends an explicit empty playerList.
	// (MPRISRequest marshals []string{} to "playerList":[]; the omitempty tag
	// would drop the key, so inject a raw body to exercise the presence check.)
	empty := newMPRISRawPacket(t, map[string]interface{}{"playerList": []string{}})
	if err := plugin.Handle(context.Background(), dev, empty); err != nil {
		t.Fatal(err)
	}

	if got := plugin.RemoteState(dev.ID()); got != nil {
		t.Fatalf("expected remote state to be pruned, got %+v", got)
	}
	expectEvent(t, sub) // empty update published
}

func TestHandleKeepsListedPlayer(t *testing.T) {
	bus := events.NewBus(zap.NewNop())
	sub := bus.Subscribe(4, events.TypeMprisUpdate)
	defer sub.Close()

	plugin := NewMPRISPlugin(nil, bus, false, zap.NewNop())
	if plugin.watchCancel != nil {
		defer plugin.watchCancel()
	}
	dev := &recordingSender{id: "device-keep"}

	state := MPRISRequest{Player: "Spotify", Title: "T", IsPlaying: true}
	if err := plugin.Handle(context.Background(), dev, newMPRISPacket(t, state)); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, sub) // initial state event

	// playerList still contains the tracked player — state must survive.
	list := newMPRISPacket(t, MPRISRequest{PlayerList: []string{"Spotify", "Namida"}})
	if err := plugin.Handle(context.Background(), dev, list); err != nil {
		t.Fatal(err)
	}

	if got := plugin.RemoteState(dev.ID()); got == nil || got.Player != "Spotify" {
		t.Fatalf("expected state to survive for listed player, got %+v", got)
	}
	expectNoEvent(t, sub) // no spurious empty update
}
