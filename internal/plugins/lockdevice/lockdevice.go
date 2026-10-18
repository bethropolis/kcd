// Package lockdevice implements the KDE Connect Lock Device plugin.
// It allows the phone to query and control the lock state of the Linux session.
package lockdevice

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/sysexec"
)

// LockDevicePlugin handles incoming lock/unlock requests from the phone.
type LockDevicePlugin struct {
	logger            log.Logger
	exec              *sysexec.Exec
	bus               *events.Bus
	allowRemoteUnlock bool
}

const sessionActionTimeout = 10 * time.Second

func NewLockDevicePlugin(cfg config.LockDeviceConfig, bus *events.Bus, logger log.Logger, executors ...*sysexec.Exec) *LockDevicePlugin {
	exec := sysexec.New(nil)
	if len(executors) > 0 && executors[0] != nil {
		exec = executors[0]
	}
	return &LockDevicePlugin{
		logger:            logger.With(log.String("plugin", "lockdevice")),
		exec:              exec,
		bus:               bus,
		allowRemoteUnlock: cfg.AllowRemoteUnlock,
	}
}

type inboundLockRequest struct {
	RequestLocked *bool `json:"requestLocked"`
	SetLocked     *bool `json:"setLocked"`
}

type inboundLockState struct {
	IsLocked *bool `json:"isLocked"`
}

type outboundLockState struct {
	IsLocked bool `json:"isLocked"`
}

func (p *LockDevicePlugin) Name() string           { return "LockDevice" }
func (p *LockDevicePlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *LockDevicePlugin) IncomingTypes() []string {
	return []string{"kdeconnect.lock", "kdeconnect.lock.request"}
}
func (p *LockDevicePlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.lock", "kdeconnect.lock.request"}
}

func (p *LockDevicePlugin) Handle(_ context.Context, dev device.Sender, pkt *protocol.Packet) error {
	switch pkt.Type {
	case "kdeconnect.lock":
		var state inboundLockState
		if err := json.Unmarshal(pkt.Body, &state); err != nil {
			return err
		}
		if state.IsLocked != nil && p.bus != nil {
			p.bus.Publish(events.TypeLockState, dev.ID(), map[string]bool{"isLocked": *state.IsLocked})
		}
		return nil
	case "kdeconnect.lock.request":
	default:
		return nil
	}
	var body inboundLockRequest
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	if body.RequestLocked != nil && *body.RequestLocked {
		go func() {
			p.sendState(dev)
		}()
		return nil
	}
	if body.SetLocked == nil {
		return nil
	}
	if !*body.SetLocked && !p.allowRemoteUnlock {
		p.logger.Warn("lockdevice: remote unlock refused", log.String("device_id", dev.ID()))
		return nil
	}

	action := "unlock-session"
	if *body.SetLocked {
		action = "lock-session"
	}
	go p.applyRequest(dev, action)
	return nil
}

func (p *LockDevicePlugin) applyRequest(dev device.Sender, action string) {
	ctx, cancel := context.WithTimeout(context.Background(), sessionActionTimeout)
	defer cancel()
	if _, err := p.exec.Run(ctx, "loginctl", action); err != nil {
		p.logger.Warn("lockdevice: session action failed", log.String("action", action), log.Error(err))
	}
	p.sendState(dev)
}

func (p *LockDevicePlugin) sendState(dev device.Sender) {
	pkt, err := protocol.NewPacket("kdeconnect.lock", outboundLockState{IsLocked: p.getLocked()})
	if err != nil {
		p.logger.Error("lockdevice: failed to create state packet", log.Error(err))
		return
	}
	if err := dev.Send(pkt); err != nil {
		p.logger.Error("lockdevice: failed to send lock state", log.Error(err))
	}
}

// getLocked queries the current session lock state via loginctl.
func (p *LockDevicePlugin) getLocked() bool {
	sessionID := os.Getenv("XDG_SESSION_ID")
	if sessionID == "" {
		sessionID = "auto" // loginctl will guess the current session
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := p.exec.Output(ctx, "loginctl", "show-session", sessionID, "-p", "LockedHint")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "LockedHint=yes"
}

// Lock asks the remote device to lock its own screen; the local session
// is never touched here (that happens only in Handle, on phone request).
func (p *LockDevicePlugin) Lock(dev device.Sender) error {
	pkt, err := protocol.NewPacket("kdeconnect.lock.request", struct {
		SetLocked bool `json:"setLocked"`
	}{SetLocked: true})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

// Unlock asks the remote device to unlock its own screen.
func (p *LockDevicePlugin) Unlock(dev device.Sender) error {
	pkt, err := protocol.NewPacket("kdeconnect.lock.request", struct {
		SetLocked bool `json:"setLocked"`
	}{SetLocked: false})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

func (p *LockDevicePlugin) OnConnect(dev device.Sender)    {}
func (p *LockDevicePlugin) OnDisconnect(dev device.Sender) {}
