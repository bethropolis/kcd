package runcommand

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
)

// RunCommandPlugin allows remote devices to trigger pre-configured local commands.
type RunCommandPlugin struct {
	// Commands maps keys to shell command strings.
	Commands map[string]string
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
		// KDE Connect expects an object where each entry is a command config.
		// The phone app parses it to show a list of labels to the user.
		// Format: {"commandList": "{\"key\": {\"name\": \"Name\", \"command\": \"/bin/sh\"}, ...}"}

		// In our simple config, keys are labels.
		list := make(map[string]map[string]string)
		for label, cmd := range p.Commands {
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
		cmdStr, ok := p.Commands[body.Key]
		if !ok {
			return nil
		}

		// Handlers must not block. Spawning goroutine.
		go func() {
			// Absolute rule: RunCommand uses exec.CommandContext with 10s timeout
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Using sh -c to allow multiple arguments in a single string from config.
			cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
			_ = cmd.Run()
		}()
	}

	return nil
}

func (p *RunCommandPlugin) OnConnect(dev device.Sender) {}

func (p *RunCommandPlugin) OnDisconnect(dev device.Sender) {
}
