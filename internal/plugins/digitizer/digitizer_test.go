package digitizer

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"sync"
	"testing"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

type mockTablet struct {
	mu     sync.Mutex
	events []DigitizerBody
	closed bool
}

func (m *mockTablet) SendEvent(body DigitizerBody) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, body)
	return nil
}

func (m *mockTablet) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

type fakePeer struct {
	id   string
	name string
}

func (f *fakePeer) ID() string                    { return f.id }
func (f *fakePeer) Name() string                  { return f.name }
func (f *fakePeer) State() device.PairingState    { return device.StatePaired }
func (f *fakePeer) Send(_ *protocol.Packet) error { return nil }
func (f *fakePeer) IsConnected() bool             { return true }
func (f *fakePeer) RemoteIP() net.IP              { return net.IPv4(127, 0, 0, 1) }
func (f *fakePeer) PeerCert() *x509.Certificate   { return nil }
func (f *fakePeer) HasCapability(cap string) bool { return true }

func TestDigitizerSessionLifecycle(t *testing.T) {
	var createdTablet *mockTablet
	mockCreator := func(w, h int32) (TabletDevice, error) {
		createdTablet = &mockTablet{}
		return createdTablet, nil
	}

	bus := events.NewBus(log.Nop())
	sub := bus.Subscribe(10, events.TypeDigitizerSession)
	defer sub.Close()

	p := NewDigitizerPlugin(bus, log.Nop(), mockCreator)
	peer := &fakePeer{id: "peer1", name: "Tablet"}

	// 1. Packets ignored without active session
	digBody, _ := json.Marshal(DigitizerBody{Tool: "Pen"})
	err := p.Handle(context.Background(), peer, &protocol.Packet{
		Type: "kdeconnect.digitizer",
		Body: digBody,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if createdTablet != nil {
		t.Fatalf("expected no tablet created without session start")
	}

	// 2. Start session
	startBody, _ := json.Marshal(SessionBody{
		Action: "start",
		Width:  1920,
		Height: 1080,
	})
	err = p.Handle(context.Background(), peer, &protocol.Packet{
		Type: "kdeconnect.digitizer.session",
		Body: startBody,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if createdTablet == nil {
		t.Fatalf("expected tablet created after session start")
	}

	evt := <-sub.C
	if evt.Type != events.TypeDigitizerSession {
		t.Fatalf("expected event %v, got %v", events.TypeDigitizerSession, evt.Type)
	}
	payload := evt.Payload.(map[string]any)
	if payload["active"] != true {
		t.Fatalf("expected active=true, got %v", payload["active"])
	}

	// 3. Send digitizer event
	active := true
	x, y := 100.0, 200.0
	pressure := 0.75
	touching := true
	eventBody, _ := json.Marshal(DigitizerBody{
		Active:   &active,
		Tool:     "Pen",
		X:        &x,
		Y:        &y,
		Pressure: &pressure,
		Touching: &touching,
	})
	err = p.Handle(context.Background(), peer, &protocol.Packet{
		Type: "kdeconnect.digitizer",
		Body: eventBody,
	})
	if err != nil {
		t.Fatalf("send digitizer event: %v", err)
	}

	createdTablet.mu.Lock()
	if len(createdTablet.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(createdTablet.events))
	}
	ev := createdTablet.events[0]
	createdTablet.mu.Unlock()

	if *ev.X != 100.0 || *ev.Y != 200.0 || *ev.Pressure != 0.75 || *ev.Touching != true {
		t.Fatalf("unexpected event data: %+v", ev)
	}

	// 4. End session
	endBody, _ := json.Marshal(SessionBody{Action: "end"})
	err = p.Handle(context.Background(), peer, &protocol.Packet{
		Type: "kdeconnect.digitizer.session",
		Body: endBody,
	})
	if err != nil {
		t.Fatalf("end session: %v", err)
	}

	createdTablet.mu.Lock()
	if !createdTablet.closed {
		t.Fatalf("expected tablet closed after session end")
	}
	createdTablet.mu.Unlock()

	evtEnd := <-sub.C
	payloadEnd := evtEnd.Payload.(map[string]any)
	if payloadEnd["active"] != false {
		t.Fatalf("expected active=false, got %v", payloadEnd["active"])
	}
}

func TestDigitizerDisconnectEndsSession(t *testing.T) {
	var createdTablet *mockTablet
	mockCreator := func(w, h int32) (TabletDevice, error) {
		createdTablet = &mockTablet{}
		return createdTablet, nil
	}

	bus := events.NewBus(log.Nop())
	p := NewDigitizerPlugin(bus, log.Nop(), mockCreator)
	peer := &fakePeer{id: "peer2", name: "Tablet2"}

	startBody, _ := json.Marshal(SessionBody{Action: "start", Width: 1280, Height: 800})
	_ = p.Handle(context.Background(), peer, &protocol.Packet{
		Type: "kdeconnect.digitizer.session",
		Body: startBody,
	})

	if createdTablet == nil {
		t.Fatalf("expected tablet created")
	}

	p.OnDisconnect(peer)

	createdTablet.mu.Lock()
	closed := createdTablet.closed
	createdTablet.mu.Unlock()

	if !closed {
		t.Fatalf("expected tablet closed on disconnect")
	}
}
