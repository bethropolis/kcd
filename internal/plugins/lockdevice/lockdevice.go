// Package lockdevice implements the KDE Connect Lock Device plugin.
// It allows the phone to query and control the lock state of the Linux session.
package lockdevice

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/sysexec"
)

// LockDevicePlugin handles incoming lock/unlock requests from the phone.
type LockDevicePlugin struct {
	logger log.Logger
	exec   *sysexec.Exec
}

func NewLockDevicePlugin(logger log.Logger, executors ...*sysexec.Exec) *LockDevicePlugin {
	exec := sysexec.New(nil)
	if len(executors) > 0 && executors[0] != nil {
		exec = executors[0]
	}
	return &LockDevicePlugin{
		logger: logger.With(log.String("plugin", "lockdevice")),
		exec:   exec,
	}
}

type LockBody struct {
	RequestLocked bool `json:"requestLocked,omitempty"`
	// Always present on the wire: absent reads as false, which a strict
	// peer cannot distinguish from a malformed unlock request.
	SetLocked bool `json:"setLocked"`
	IsLocked  bool `json:"isLocked,omitempty"`
}

func (p *LockDevicePlugin) Name() string           { return "LockDevice" }
func (p *LockDevicePlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *LockDevicePlugin) IncomingTypes() []string {
	return []string{"kdeconnect.lock", "kdeconnect.lock.request"}
}
func (p *LockDevicePlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.lock", "kdeconnect.lock.request"}
}

func (p *LockDevicePlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body LockBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	// Phone is requesting the current lock state.
	if body.RequestLocked {
		go func() {
			locked := p.getLocked()
			pkt, err := protocol.NewPacket("kdeconnect.lock", LockBody{IsLocked: locked})
			if err != nil {
				p.logger.Error("lockdevice: failed to create reply packet", log.Error(err))
				return
			}
			if err := dev.Send(pkt); err != nil {
				p.logger.Error("lockdevice: failed to send lock state", log.Error(err))
			}
		}()
		return nil
	}

	// Only an explicit lock request acts locally; a peer's bare state
	// report must never lock or unlock anything. Failures are logged.
	if pkt.Type != "kdeconnect.lock.request" {
		return nil
	}
	if body.SetLocked {
		p.exec.RunAsync(p.logger, "loginctl", "lock-session")
	} else {
		p.exec.RunAsync(p.logger, "loginctl", "unlock-session")
	}

	return nil
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
	pkt, err := protocol.NewPacket("kdeconnect.lock.request", LockBody{SetLocked: true})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

// Unlock asks the remote device to unlock its own screen.
func (p *LockDevicePlugin) Unlock(dev device.Sender) error {
	pkt, err := protocol.NewPacket("kdeconnect.lock.request", LockBody{SetLocked: false})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

func (p *LockDevicePlugin) OnConnect(dev device.Sender)    {}
func (p *LockDevicePlugin) OnDisconnect(dev device.Sender) {}
