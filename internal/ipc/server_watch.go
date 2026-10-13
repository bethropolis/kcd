package ipc

import (
	"encoding/json"
	"net"
	"time"

	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/plugins/connectivity"
	"github.com/bethropolis/kcd/internal/plugins/mpris"
)

func (s *Server) handleWatch(conn net.Conn, payload []byte) {
	defer conn.Close()

	var p WatchPayload
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}

	bus := s.handler.bus
	if bus == nil {
		s.writeResponse(conn, Response{OK: false, Error: "event bus not enabled"})
		return
	}

	s.writeResponse(conn, Response{OK: true})

	// Full-state snapshot first so clients boot complete from this connection
	// alone — no bootstrap calls, no hydration races. Sent despite filters.
	snapEv := map[string]interface{}{
		"type":      events.TypeStateSnapshot,
		"timestamp": time.Now().UTC(),
		"payload":   BuildSnapshot(s.handler.devices, s.handler.plugins),
	}
	data, _ := json.Marshal(snapEv)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return
	}

	// Initial State Dump
	// For each connected device, we emit device.connected and battery.update.
	devs := s.handler.devices.Connected()
	for _, dev := range devs {
		devData := map[string]interface{}{
			"id":   dev.ID(),
			"name": dev.Name(),
			"type": dev.Type,
		}

		initEv := map[string]interface{}{
			"type":      "device.connected",
			"deviceId":  dev.ID(),
			"timestamp": time.Now().UTC(),
			"payload":   devData,
		}
		data, _ = json.Marshal(initEv)
		data = append(data, '\n')
		if _, err := conn.Write(data); err != nil {
			return
		}

		// Only with an actual reading: a fresh pair would otherwise publish
		// a bogus stable 0%. Same skip-if-absent rule as connectivity.
		if dev.HasBattery() {
			charge, charging := dev.GetBattery()
			batEv := map[string]interface{}{
				"type":      "battery.update",
				"deviceId":  dev.ID(),
				"timestamp": time.Now().UTC(),
				"payload": map[string]interface{}{
					"charge":       charge,
					"charging":     charging,
					"batteryAgeMs": dev.BatteryAge().Milliseconds(),
				},
			}
			data, _ = json.Marshal(batEv)
			data = append(data, '\n')
			if _, err := conn.Write(data); err != nil {
				return
			}
		}

		// Send initial connectivity only if reported: unlike battery it has
		// no meaningful zero, so absence skips instead of emitting empty.
		if pl, ok := s.handler.plugins.GetByName("Connectivity"); ok {
			if report, ok := pl.(*connectivity.ConnectivityPlugin).Report(dev.ID()); ok {
				connEv := map[string]interface{}{
					"type":      "connectivity.update",
					"deviceId":  dev.ID(),
					"timestamp": time.Now().UTC(),
					"payload":   report,
				}
				data, _ = json.Marshal(connEv)
				data = append(data, '\n')
				if _, err := conn.Write(data); err != nil {
					return
				}
			}
		}

		// Send initial mpris state only if fresh (10s): older means playback
		// likely stopped, and stale state shows a ghost "now playing".
		if pl, ok := s.handler.plugins.GetByName("MPRIS"); ok {
			mp := pl.(*mpris.MPRISPlugin)
			if mp.RemoteStateAge(dev.ID()) < 10*time.Second {
				if state := mp.RemoteState(dev.ID()); state != nil {
					mprisEv := map[string]interface{}{
						"type":      "mpris.update",
						"deviceId":  dev.ID(),
						"timestamp": time.Now().UTC(),
						"payload":   state,
					}
					data, _ = json.Marshal(mprisEv)
					data = append(data, '\n')
					if _, err := conn.Write(data); err != nil {
						return
					}
				}
			}
		}
	}

	// An empty filter matches everything except the opt-in types, which
	// Subscriber.matches handles — see events.OptInOnly.
	var filters []events.EventType
	for _, e := range p.Events {
		filters = append(filters, events.EventType(e))
	}

	sub := bus.Subscribe(events.WatchSubscriberCap, filters...)
	defer sub.Close()

	for ev := range sub.C {
		data, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		data = append(data, '\n')
		if _, err := conn.Write(data); err != nil {
			return // connection likely closed
		}
	}
}
