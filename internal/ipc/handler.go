package ipc

import (
	"encoding/json"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/pair"
	"github.com/bethropolis/kcd/internal/protocol"
)

// Handler handles incoming IPC requests.
type Handler struct {
	devices    *device.Registry
	plugins    *plugin.Registry
	pairPlugin *pair.PairPlugin
	statePath  string
	bus        *events.Bus
	routes     map[string]func(Request) Response
}

// NewHandler creates a new IPC command handler.
func NewHandler(devices *device.Registry, plugins *plugin.Registry, pairPlugin *pair.PairPlugin, statePath string, bus *events.Bus) *Handler {
	return &Handler{
		devices:    devices,
		plugins:    plugins,
		pairPlugin: pairPlugin,
		statePath:  statePath,
		bus:        bus,
		routes:     make(map[string]func(Request) Response),
	}
}

// Register adds a custom handler for a given command.
func (h *Handler) Register(command string, fn func(Request) Response) {
	h.routes[command] = fn
}

// HandleRequest processes an incoming IPC request and returns a response.
func (h *Handler) HandleRequest(req Request) Response {
	if fn, ok := h.routes[req.Command]; ok {
		return fn(req)
	}

	switch req.Command {
	case CmdDevices:
		return h.handleDevices()
	case CmdPair:
		return h.handlePair(req.Payload)
	case CmdPairListen:
		return h.handlePairListen()
	case CmdUnpair:
		return h.handleUnpair(req.Payload)
	case CmdPing:
		return h.handlePing(req.Payload)
	default:
		return Response{OK: false, Error: "unknown command"}
	}
}

func (h *Handler) handleDevices() Response {
	// To convert the internal representation to JSON, we construct DeviceInfo structs
	devs := h.devices.List()
	infos := make([]device.DeviceInfo, 0, len(devs))
	for _, dev := range devs {
		infos = append(infos, device.DeviceInfo{
			ID:        dev.ID(),
			Name:      dev.Name(),
			Type:      dev.Type,
			State:     dev.State(),
			Connected: dev.IsConnected(),
		})
	}

	data, err := json.Marshal(infos)
	if err != nil {
		return Response{OK: false, Error: "failed to marshal device list"}
	}
	return Response{OK: true, Data: data}
}

func (h *Handler) saveDevices() {
	if h.statePath == "" {
		return
	}
	devs := h.devices.List()
	infos := make([]device.DeviceInfo, 0, len(devs))
	for _, dev := range devs {
		infos = append(infos, device.DeviceInfo{
			ID:     dev.ID(),
			Name:   dev.Name(),
			Type:   dev.Type,
			State:  dev.State(),
			CertFP: dev.CertFP,
		})
	}
	_ = device.SaveDevices(h.statePath, infos)
}

func (h *Handler) handlePair(payload []byte) Response {
	var p DevicePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return Response{OK: false, Error: "invalid payload"}
	}

	dev, ok := h.devices.Get(p.DeviceID)
	if !ok {
		return Response{OK: false, Error: "device not found"}
	}

	// Use the pair plugin to handle pairing properly
	if h.pairPlugin != nil {
		state := dev.State()
		if state == device.StatePairRequestedByPeer {
			// Accept pending request
			if err := h.pairPlugin.AcceptPairing(dev); err != nil {
				return Response{OK: false, Error: "failed to accept pairing: " + err.Error()}
			}
		} else {
			// Initiate new pairing request
			if err := h.pairPlugin.RequestPairing(dev); err != nil {
				return Response{OK: false, Error: "failed to request pairing: " + err.Error()}
			}
		}
		return Response{OK: true}
	}

	// Fallback if no pair plugin (shouldn't happen)
	pkt, _ := protocol.NewPairPacket(protocol.PairAccept)
	if err := dev.Send(pkt); err != nil {
		return Response{OK: false, Error: "failed to send pair packet"}
	}

	dev.SetState(device.StatePaired)
	h.saveDevices()

	return Response{OK: true}
}

func (h *Handler) handleUnpair(payload []byte) Response {
	var p DevicePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return Response{OK: false, Error: "invalid payload"}
	}

	dev, ok := h.devices.Get(p.DeviceID)
	if !ok {
		return Response{OK: false, Error: "device not found"}
	}

	// Use the pair plugin to handle unpairing properly
	if h.pairPlugin != nil {
		if err := h.pairPlugin.Unpair(dev); err != nil {
			return Response{OK: false, Error: "failed to unpair: " + err.Error()}
		}
		return Response{OK: true}
	}

	// Fallback
	pkt, _ := protocol.NewPairPacket(protocol.PairReject)
	_ = dev.Send(pkt)
	h.devices.Remove(p.DeviceID)
	h.saveDevices()

	return Response{OK: true}
}

func (h *Handler) handlePairListen() Response {
	// Check for any device already in StatePairRequestedByPeer
	for _, dev := range h.devices.List() {
		if dev.State() == device.StatePairRequestedByPeer {
			return h.pairAcceptResult(dev, "")
		}
	}

	if h.bus == nil || h.pairPlugin == nil {
		return Response{OK: false, Error: "pair listen requires event bus and pair plugin"}
	}

	sub := h.bus.Subscribe(0, events.TypePairRequested)
	defer sub.Close()

	select {
	case ev, ok := <-sub.C:
		if !ok {
			return Response{OK: false, Error: "event bus closed"}
		}
		dev, ok := h.devices.Get(ev.DeviceID)
		if !ok {
			return Response{OK: false, Error: "device disappeared"}
		}
		var vKey string
		if m, ok := ev.Payload.(map[string]interface{}); ok {
			if k, ok := m["verificationKey"].(string); ok {
				vKey = k
			}
		}
		return h.pairAcceptResult(dev, vKey)
	case <-time.After(60 * time.Second):
		return Response{OK: false, Error: "timed out waiting for pair request (60s)"}
	}
}

func (h *Handler) pairAcceptResult(dev *device.Device, vKey string) Response {
	if err := h.pairPlugin.AcceptPairing(dev); err != nil {
		return Response{OK: false, Error: "failed to accept pairing: " + err.Error()}
	}
	data, _ := json.Marshal(PairListenResult{
		DeviceID:        dev.ID(),
		DeviceName:      dev.Name(),
		VerificationKey: vKey,
	})
	return Response{OK: true, Data: data}
}

func (h *Handler) handlePing(payload []byte) Response {
	var p DevicePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return Response{OK: false, Error: "invalid payload"}
	}

	dev, ok := h.devices.Get(p.DeviceID)
	if !ok {
		return Response{OK: false, Error: "device not found"}
	}

	// Ping packet is actually kdeconnect.ping
	pkt, _ := protocol.NewPacket("kdeconnect.ping", map[string]string{"message": "Ping!"})
	if err := dev.Send(pkt); err != nil {
		return Response{OK: false, Error: "failed to send ping packet"}
	}
	return Response{OK: true}
}
