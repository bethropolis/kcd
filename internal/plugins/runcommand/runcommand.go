package runcommand

import (
	"context"
	"encoding/json"
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
	return []string{"kdeconnect.runcommand"}
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
		// Handlers must not block. Spawning goroutine.
		plugin.RunCommandAsync(p.logger, "sh", "-c", cmdStr)
	}

	return nil
}

func (p *RunCommandPlugin) OnConnect(dev device.Sender) {}

func (p *RunCommandPlugin) OnDisconnect(dev device.Sender) {
}
