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
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
)

// LockDevicePlugin handles incoming lock/unlock requests from the phone.
type LockDevicePlugin struct {
	logger log.Logger
}

func NewLockDevicePlugin(logger log.Logger) *LockDevicePlugin {
	return &LockDevicePlugin{
		logger: logger.With(log.String("plugin", "lockdevice")),
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
				p.logger.Error("lockdevice: failed to create reply packet", log.Error(err))
				return
			}
			if err := dev.Send(pkt); err != nil {
				p.logger.Error("lockdevice: failed to send lock state", log.Error(err))
			}
		}()
		return nil
	}

	// Phone is requesting a lock/unlock action. Fire and forget so Handle
	// returns immediately; failures are logged by the seam.
	if body.SetLocked {
		plugin.RunCommandAsync(p.logger, "loginctl", "lock-session")
	} else {
		plugin.RunCommandAsync(p.logger, "loginctl", "unlock-session")
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

	out, err := plugin.RunCommandOutput(ctx, "loginctl", "show-session", sessionID, "-p", "LockedHint")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "LockedHint=yes"
}

// Lock triggers an immediate session lock from the daemon/IPC side.
func (p *LockDevicePlugin) Lock(dev device.Sender) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := plugin.RunCommandSync(ctx, "loginctl", "lock-session")
	return err
}

// Unlock triggers an immediate session unlock from the daemon/IPC side.
func (p *LockDevicePlugin) Unlock(dev device.Sender) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := plugin.RunCommandSync(ctx, "loginctl", "unlock-session")
	return err
}

func (p *LockDevicePlugin) OnConnect(dev device.Sender)    {}
func (p *LockDevicePlugin) OnDisconnect(dev device.Sender) {}
