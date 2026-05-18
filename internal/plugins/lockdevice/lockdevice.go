// Package lockdevice implements the KDE Connect Lock Device plugin.
// It allows the phone to query and control the lock state of the Linux session.
package lockdevice

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// LockDevicePlugin handles incoming lock/unlock requests from the phone.
type LockDevicePlugin struct {
	logger *zap.Logger
}

func NewLockDevicePlugin(logger *zap.Logger) *LockDevicePlugin {
	return &LockDevicePlugin{
		logger: logger.With(zap.String("plugin", "lockdevice")),
	}
}

type LockBody struct {
	RequestLocked bool `json:"requestLocked,omitempty"`
	SetLocked     bool `json:"setLocked,omitempty"`
	IsLocked      bool `json:"isLocked,omitempty"`
}

func (p *LockDevicePlugin) Name() string           { return "LockDevice" }
func (p *LockDevicePlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *LockDevicePlugin) IncomingTypes() []string {
	return []string{"kdeconnect.lock", "kdeconnect.lock.request"}
}
func (p *LockDevicePlugin) OutgoingTypes() []string { return []string{"kdeconnect.lock"} }

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
				p.logger.Error("lockdevice: failed to create reply packet", zap.Error(err))
				return
			}
			if err := dev.Send(pkt); err != nil {
				p.logger.Error("lockdevice: failed to send lock state", zap.Error(err))
			}
		}()
		return nil
	}

	// Phone is requesting a lock/unlock action.
	go func() {
		if body.SetLocked {
			if err := exec.CommandContext(context.Background(), "loginctl", "lock-session").Run(); err != nil {
				p.logger.Warn("lockdevice: lock-session failed", zap.Error(err))
			}
		} else {
			if err := exec.CommandContext(context.Background(), "loginctl", "unlock-session").Run(); err != nil {
				p.logger.Warn("lockdevice: unlock-session failed", zap.Error(err))
			}
		}
	}()

	return nil
}

// getLocked queries the current session lock state via loginctl.
func (p *LockDevicePlugin) getLocked() bool {
	sessionID := os.Getenv("XDG_SESSION_ID")
	if sessionID == "" {
		sessionID = "auto" // loginctl will guess the current session
	}
	out, err := exec.CommandContext(context.Background(), "loginctl", "show-session", sessionID, "-p", "LockedHint").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "LockedHint=yes"
}

// Lock triggers an immediate session lock from the daemon/IPC side.
func (p *LockDevicePlugin) Lock(dev device.Sender) error {
	return exec.CommandContext(context.Background(), "loginctl", "lock-session").Run()
}

// Unlock triggers an immediate session unlock from the daemon/IPC side.
func (p *LockDevicePlugin) Unlock(dev device.Sender) error {
	return exec.CommandContext(context.Background(), "loginctl", "unlock-session").Run()
}

func (p *LockDevicePlugin) OnConnect(dev device.Sender)    {}
func (p *LockDevicePlugin) OnDisconnect(dev device.Sender) {}
