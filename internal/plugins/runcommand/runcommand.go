package runcommand

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// RunCommandPlugin allows remote devices to trigger pre-configured local commands.
type RunCommandPlugin struct {
	Mu                sync.RWMutex // exported so daemon.go can lock it during reload
	Commands          map[string]string
	CommandsPerDevice map[string]map[string]string // keyed by device ID
	// pendingLists holds one buffered waiter per device awaiting that
	// device's command-list reply, keyed by device ID.
	pendingLists map[string]chan []Command
	// running holds every live execution's cancel func (device, then id) so
	// the phone's stop button reaches it; entries drop on finish.
	running map[string]map[int32]context.CancelFunc
	// execSeq hands out execution ids as a counter: the phone reads the id
	// with getInt, which would overflow on nanoseconds.
	execSeq int32
	bus     *events.Bus
	logger  log.Logger
	wg      sync.WaitGroup // exported for tests to synchronize with background goroutines
}

func NewRunCommandPlugin(commands map[string]string, commandsPerDevice map[string]map[string]string, bus *events.Bus, logger log.Logger) *RunCommandPlugin {
	if commandsPerDevice == nil {
		commandsPerDevice = make(map[string]map[string]string)
	}
	return &RunCommandPlugin{
		Commands:          commands,
		CommandsPerDevice: commandsPerDevice,
		pendingLists:      make(map[string]chan []Command),
		running:           make(map[string]map[int32]context.CancelFunc),
		bus:               bus,
		logger:            logger.With(log.String("plugin", "runcommand")),
	}
}

// RequestBody represents a request from the phone.
type RequestBody struct {
	RequestCommandList bool   `json:"requestCommandList,omitempty"`
	Key                string `json:"key,omitempty"`
	// Stop cancels a running execution. The phone's stop button sends this
	// with the execution id.
	Stop bool  `json:"stop,omitempty"`
	ID   int32 `json:"id,omitempty"`
}

// Name returns the plugin name.
func (p *RunCommandPlugin) Name() string { return "RunCommand" }

// Timeout returns the timeout.
func (p *RunCommandPlugin) Timeout() time.Duration { return 5 * time.Second }

// IncomingTypes returns the packet types handled here. The bare runcommand
// type carries the list reply; without it the reply drops as unhandled.
func (p *RunCommandPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.runcommand.request", "kdeconnect.runcommand"}
}

// OutgoingTypes returns the packet types this plugin may send. The output
// type advertises the streaming path Android notifications can't carry.
func (p *RunCommandPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.runcommand", PacketTypeOutput, "kdeconnect.notification"}
}

// Handle processes incoming command requests.
func (p *RunCommandPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	if pkt.Type == "kdeconnect.runcommand" {
		p.handleListReply(dev, pkt)
		return nil
	}

	var body RequestBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	// The phone's stop button sends {"stop": true} with the execution id.
	if body.Stop {
		p.stopRunning(dev.ID(), body.ID)
		return nil
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

		cmdStr, ok := perDev[body.Key]
		if !ok {
			cmdStr, ok = cmds[body.Key]
		}
		if !ok {
			return nil
		}

		// Handlers must not block: run in a goroutine, streaming to the output
		// card with a notification fallback for users without it enabled.
		execID := p.nextExecID()
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			execCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			p.Mu.Lock()
			if p.running[dev.ID()] == nil {
				p.running[dev.ID()] = make(map[int32]context.CancelFunc)
			}
			p.running[dev.ID()][execID] = cancel
			p.Mu.Unlock()

			// Registered before the command starts so the phone's stop
			// button can reach an execution that has just been launched.
			if err := p.sendStarted(dev, execID, body.Key); err != nil {
				p.logger.Warn("failed to send command started", log.Error(err))
			}

			cmd := exec.CommandContext(execCtx, "sh", "-c", cmdStr)
			text := p.streamOutput(execCtx, dev, execID, body.Key, cmd)

			// Keep the notification path: it is the only channel for anyone
			// who has not enabled the phone's in-app output card.
			text = strings.TrimSpace(text)
			if len(text) == 0 {
				return
			}

			// Do not send notifications for massive outputs (e.g. log dumps)
			if len(text) > 4096 {
				p.logger.Warn("command output too large for notification, truncating", log.Int("len", len(text)))
				text = text[:4000] + "\n...[output truncated]"
			}

			// Sent as a notification: Android uses appName/ticker as title/body,
			// ignoring title and text.
			notifBody := map[string]interface{}{
				"id":      fmt.Sprintf("%d", time.Now().UnixNano()),
				"appName": fmt.Sprintf("Run: %s", body.Key),
				"ticker":  text,
			}

			p.logger.Debug("sending command output notification",
				log.String("key", body.Key),
				log.Int("output_len", len(text)),
			)
			if pkt, err := protocol.NewPacket("kdeconnect.notification", notifBody); err == nil {
				if err := dev.Send(pkt); err != nil {
					p.logger.Warn("failed to send command output notification",
						log.String("key", body.Key),
						log.Error(err),
					)
				}
			}
		}()
	}

	return nil
}

// sendStarted announces a beginning execution. The phone registers the id
// against a display row here, so it must precede any output for that id.
func (p *RunCommandPlugin) sendStarted(dev device.Sender, id int32, command string) error {
	pkt, err := protocol.NewPacket(PacketTypeOutput, map[string]any{
		"commandStarted": true,
		"id":             id,
		"command":        command,
	})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

func (p *RunCommandPlugin) OnConnect(dev device.Sender) {}

// OnDisconnect cancels anything still running for the device, so a command
// does not outlive the connection that asked for it.
func (p *RunCommandPlugin) OnDisconnect(dev device.Sender) {
	p.stopAll(dev.ID())
}
