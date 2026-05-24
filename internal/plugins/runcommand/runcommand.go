package runcommand

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// RunCommandPlugin allows remote devices to trigger pre-configured local commands.
type RunCommandPlugin struct {
	Mu                sync.RWMutex // exported so daemon.go can lock it during reload
	Commands          map[string]string
	CommandsPerDevice map[string]map[string]string // keyed by device ID
	logger            *zap.Logger
}

func NewRunCommandPlugin(commands map[string]string, commandsPerDevice map[string]map[string]string, logger *zap.Logger) *RunCommandPlugin {
	if commandsPerDevice == nil {
		commandsPerDevice = make(map[string]map[string]string)
	}
	return &RunCommandPlugin{
		Commands:          commands,
		CommandsPerDevice: commandsPerDevice,
		logger:            logger.With(zap.String("plugin", "runcommand")),
	}
}

// RequestBody represents a request from the phone.
type RequestBody struct {
	RequestCommandList bool   `json:"requestCommandList,omitempty"`
	Key                string `json:"key,omitempty"`
}

// Name returns the plugin name.
func (p *RunCommandPlugin) Name() string { return "RunCommand" }

// Timeout returns the timeout.
func (p *RunCommandPlugin) Timeout() time.Duration { return 5 * time.Second }

// IncomingTypes returns the packet types this plugin handles.
func (p *RunCommandPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.runcommand.request"}
}

// OutgoingTypes returns the packet types this plugin may send.
func (p *RunCommandPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.runcommand", "kdeconnect.notification"}
}

// Handle processes incoming command requests.
func (p *RunCommandPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body RequestBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	if body.RequestCommandList {
		p.Mu.RLock()
		cmds := p.Commands
		perDev := p.CommandsPerDevice[dev.ID()]
		p.Mu.RUnlock()

		// Merge global + per-device commands. Per-device overrides global.
		list := make(map[string]map[string]string)
		for label, cmd := range cmds {
			list[label] = map[string]string{
				"name":    label,
				"command": cmd,
			}
		}
		for label, cmd := range perDev {
			list[label] = map[string]string{
				"name":    label,
				"command": cmd,
			}
		}

		listBytes, _ := json.Marshal(list)
		res, err := protocol.NewPacket("kdeconnect.runcommand", map[string]string{
			"commandList": string(listBytes),
		})
		if err != nil {
			return err
		}
		return dev.Send(res)
	}

	if body.Key != "" {
		p.Mu.RLock()
		perDev := p.CommandsPerDevice[dev.ID()]
		cmds := p.Commands
		p.Mu.RUnlock()

		// Check per-device first, then global.
		cmdStr, ok := perDev[body.Key]
		if !ok {
			cmdStr, ok = cmds[body.Key]
		}
		if !ok {
			return nil
		}

		// Handlers must not block. Spawning goroutine to run the command
		// and optionally send a notification with the output.
		go func() {
			execCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			out, err := plugin.RunCommandSync(execCtx, "sh", "-c", cmdStr)

			text := strings.TrimSpace(string(out))
			if len(text) == 0 {
				if err != nil {
					text = fmt.Sprintf("Error: %v", err)
				} else {
					// No output and no error — do not send a notification.
					return
				}
			} else if err != nil {
				text = fmt.Sprintf("Error: %v\n\n%s", err, text)
			}

			// Do not send notifications for massive outputs (e.g. log dumps)
			if len(text) > 4096 {
				p.logger.Warn("command output too large for notification, truncating", zap.Int("len", len(text)))
				text = text[:4000] + "\n...[output truncated]"
			}

			// Send notification back to the phone
			// The Android app uses 'appName' as the title and 'ticker' as the body.
			// It ignores 'title' and 'text'.
			notifBody := map[string]interface{}{
				"id":      fmt.Sprintf("%d", time.Now().UnixNano()),
				"appName": fmt.Sprintf("Run: %s", body.Key),
				"ticker":  text,
			}

			if pkt, err := protocol.NewPacket("kdeconnect.notification", notifBody); err == nil {
				_ = dev.Send(pkt)
			}
		}()
	}

	return nil
}

func (p *RunCommandPlugin) OnConnect(dev device.Sender) {}

func (p *RunCommandPlugin) OnDisconnect(dev device.Sender) {
}
