package daemon

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/contacts"
	"github.com/bethropolis/kcd/internal/plugins/pair"
	"github.com/bethropolis/kcd/internal/protocol"
)

const defaultPairListenTimeout = 60 * time.Second

type pairHandler struct {
	devices           *device.Registry
	plugins           *plugin.Registry
	bus               *events.Bus
	saveDevicesFn     func() error
	pruneThreshold    time.Duration
	pairListenTimeout time.Duration
	pairDialHook      func(deviceID string) error
}

func registerPairRoutes(handler *ipc.Handler, cfg *config.Config, devices *device.Registry, plugins *plugin.Registry, bus *events.Bus, saveDevices func() error, pruneThreshold time.Duration, pairDialHook func(deviceID string) error) {
	listenTimeout := defaultPairListenTimeout
	if cfg != nil && cfg.Pairing.ListenTimeout != "" {
		if d := config.Duration(cfg.Pairing.ListenTimeout); d > 0 {
			listenTimeout = d
		}
	}
	h := &pairHandler{
		devices:           devices,
		plugins:           plugins,
		bus:               bus,
		saveDevicesFn:     saveDevices,
		pruneThreshold:    pruneThreshold,
		pairListenTimeout: listenTimeout,
		pairDialHook:      pairDialHook,
	}

	handler.Register(ipc.CmdPair, func(req ipc.Request) ipc.Response {
		return h.handlePair(req.Payload)
	})
	handler.Register(ipc.CmdUnpair, func(req ipc.Request) ipc.Response {
		return h.handleUnpair(req.Payload)
	})
	handler.Register(ipc.CmdPairListen, func(req ipc.Request) ipc.Response {
		return h.handlePairListen()
	})
}

func (h *pairHandler) saveDevices() error {
	if h.saveDevicesFn == nil {
		return nil
	}
	h.devices.Prune(h.pruneThreshold)
	return h.saveDevicesFn()
}

func (h *pairHandler) handlePair(payload []byte) ipc.Response {
	var p ipc.PairPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return ipc.Err(ipc.CodeInvalidPayload, "invalid payload", nil)
	}
	// Declining a request and unpairing send the same pair=false packet.
	if p.Reject {
		return h.handleUnpair(payload)
	}

	dev, ok := h.devices.Get(p.DeviceID)
	if !ok {
		return ipc.Err(ipc.CodeDeviceNotFound, "device not found", map[string]string{"deviceId": p.DeviceID})
	}

	// Pairing needs a live TLS connection, which background dial never opens
	// to strangers: an explicit pair triggers the on-demand hook, else the
	// legacy paths below.
	if !dev.IsConnected() && h.pairDialHook != nil {
		if err := h.pairDialHook(dev.ID()); err != nil {
			return ipc.Response{OK: false, Error: err.Error()}
		}
		return ipc.Response{OK: true}
	}

	if pairPl, ok := plugin.Lookup[*pair.PairPlugin](h.plugins); ok && pairPl != nil {
		state := dev.State()
		if state == device.StatePairRequestedByPeer {
			if err := pairPl.AcceptPairing(dev); err != nil {
				return ipc.Response{OK: false, Error: "failed to accept pairing: " + err.Error()}
			}
		} else {
			// Initiate a new pairing request; the code lets the CLI show what
			// to compare against the phone's prompt (else no out-of-band check).
			verificationKey, err := pairPl.RequestPairing(dev)
			if err != nil {
				return ipc.Response{OK: false, Error: "failed to request pairing: " + err.Error()}
			}
			data, _ := json.Marshal(ipc.PairResult{VerificationKey: verificationKey})
			return ipc.Response{OK: true, Data: data}
		}
		return ipc.Response{OK: true}
	}

	// No pair plugin registered; send the bare packet.
	pkt, _ := protocol.NewPairPacket(protocol.PairAccept, 0)
	if err := dev.Send(pkt); err != nil {
		return ipc.Response{OK: false, Error: "failed to send pair packet"}
	}

	dev.SetState(device.StatePaired)
	if err := h.saveDevices(); err != nil {
		return ipc.Response{OK: false, Error: "failed to save device state"}
	}

	return ipc.Response{OK: true}
}

func (h *pairHandler) handleUnpair(payload []byte) ipc.Response {
	var p ipc.DevicePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return ipc.Err(ipc.CodeInvalidPayload, "invalid payload", nil)
	}

	dev, ok := h.devices.Get(p.DeviceID)
	if !ok {
		return ipc.Err(ipc.CodeDeviceNotFound, "device not found", map[string]string{"deviceId": p.DeviceID})
	}

	if pairPl, ok := plugin.Lookup[*pair.PairPlugin](h.plugins); ok && pairPl != nil {
		if err := pairPl.Unpair(dev); err != nil {
			return ipc.Response{OK: false, Error: "failed to unpair: " + err.Error()}
		}
		dev.Disconnect()
		h.forgetContacts(p.DeviceID)
		return ipc.Response{OK: true}
	}

	// Fallback
	pkt, _ := protocol.NewPairPacket(protocol.PairReject, 0)
	_ = dev.Send(pkt)
	dev.Disconnect()
	h.devices.Remove(p.DeviceID)
	h.forgetContacts(p.DeviceID)
	if err := h.saveDevices(); err != nil {
		return ipc.Response{OK: false, Error: "failed to save device state"}
	}

	return ipc.Response{OK: true}
}

// forgetContacts drops a device's cached contacts on unpair: revoked trust
// drops the address book. Absent plugin or cache is a no-op.
func (h *pairHandler) forgetContacts(deviceID string) {
	if cpl, ok := plugin.Lookup[*contacts.ContactsPlugin](h.plugins); ok && cpl != nil {
		_ = cpl.ForgetDevice(deviceID)
	}
}

func (h *pairHandler) handlePairListen() ipc.Response {
	timeout := h.pairListenTimeout
	if timeout <= 0 {
		timeout = defaultPairListenTimeout
	}
	// Report candidates in StatePairRequestedByPeer WITHOUT accepting: the
	// caller decides (CmdPair/CmdUnpair); auto-accept would pair stale ones.
	for _, dev := range h.devices.List() {
		if dev.State() == device.StatePairRequestedByPeer {
			return h.pairListenResult(dev, "")
		}
	}

	pairPl, _ := plugin.Lookup[*pair.PairPlugin](h.plugins)
	if h.bus == nil || pairPl == nil {
		return ipc.Response{OK: false, Error: "pair listen requires event bus and pair plugin"}
	}

	sub := h.bus.Subscribe(0, events.TypePairRequested)
	defer sub.Close()

	select {
	case ev, ok := <-sub.C:
		if !ok {
			return ipc.Response{OK: false, Error: "event bus closed"}
		}
		dev, ok := h.devices.Get(ev.DeviceID)
		if !ok {
			return ipc.Response{OK: false, Error: "device disappeared"}
		}
		var vKey string
		if m, ok := ev.Payload.(map[string]interface{}); ok {
			if k, ok := m["verificationKey"].(string); ok {
				vKey = k
			}
		}
		return h.pairListenResult(dev, vKey)
	case <-time.After(timeout):
		return ipc.Response{OK: false, Error: fmt.Sprintf("timed out waiting for pair request (%s)", timeout)}
	}
}

// pairListenResult returns the candidate without accepting it; the caller
// decides via CmdPair or CmdUnpair.
func (h *pairHandler) pairListenResult(dev *device.Device, vKey string) ipc.Response {
	data, _ := json.Marshal(ipc.PairListenResult{
		DeviceID:        dev.ID(),
		DeviceName:      dev.Name(),
		VerificationKey: vKey,
		Fingerprint:     dev.CertFP,
	})
	return ipc.Response{OK: true, Data: data}
}
