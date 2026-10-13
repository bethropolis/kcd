package runcommand

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// listTimeout bounds the wait for the command-list reply; the phone answers
// from a handler, so this only fires for a closed app instead of hanging.
const listTimeout = 10 * time.Second

// Command is one remote-executable entry as advertised by the phone.
type Command struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// commandEntry is the per-label object inside the wire commandList map.
type commandEntry struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// RequestList asks the device for its command list, registering the waiter
// first so a fast phone cannot answer into a void. Fails on disconnect,
// send error, or listTimeout.
func (p *RunCommandPlugin) RequestList(ctx context.Context, dev device.Sender) ([]Command, error) {
	waiter := make(chan []Command, 1)

	p.Mu.Lock()
	if p.pendingLists == nil {
		p.pendingLists = make(map[string]chan []Command)
	}
	// A second concurrent request for the same device would race for the
	// single reply; fail loudly rather than silently dropping one.
	if _, busy := p.pendingLists[dev.ID()]; busy {
		p.Mu.Unlock()
		return nil, fmt.Errorf("runcommand: a command list request for %s is already in flight", dev.ID())
	}
	p.pendingLists[dev.ID()] = waiter
	p.Mu.Unlock()

	defer p.clearListWaiter(dev.ID())

	pkt, err := protocol.NewPacket("kdeconnect.runcommand.request", RequestBody{RequestCommandList: true})
	if err != nil {
		return nil, fmt.Errorf("runcommand: build list request: %w", err)
	}
	if err := dev.Send(pkt); err != nil {
		return nil, fmt.Errorf("runcommand: send list request: %w", err)
	}

	deadline, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()

	select {
	case commands := <-waiter:
		return commands, nil
	case <-deadline.Done():
		return nil, fmt.Errorf("runcommand: timed out after %s waiting for the command list — is the KDE Connect app open on the phone?", listTimeout)
	}
}

// clearListWaiter drops a registered waiter so a later request is not
// blocked by an abandoned one.
func (p *RunCommandPlugin) clearListWaiter(deviceID string) {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	delete(p.pendingLists, deviceID)
}

// deliverCommandList hands a decoded reply to the waiter, non-blocking: a
// waiterless reply must not stall the device's read loop.
func (p *RunCommandPlugin) deliverCommandList(deviceID string, commands []Command) {
	p.Mu.RLock()
	waiter := p.pendingLists[deviceID]
	p.Mu.RUnlock()

	if waiter == nil {
		p.logger.Debug("runcommand: command list with no waiter, dropping",
			log.String("device_id", deviceID),
			log.Int("commands", len(commands)))
		return
	}
	select {
	case waiter <- commands:
	default:
	}
}

// parseCommandList decodes the label -> {name, command} reply object,
// skipping malformed entries rather than failing the whole list.
func parseCommandList(raw string) ([]Command, error) {
	if raw == "" {
		return nil, nil
	}
	var entries map[string]commandEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("runcommand: parse command list: %w", err)
	}

	commands := make([]Command, 0, len(entries))
	for label, entry := range entries {
		name := entry.Name
		if name == "" {
			name = label
		}
		commands = append(commands, Command{Name: name, Command: entry.Command})
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	return commands, nil
}

// handleListReply processes a kdeconnect.runcommand packet carrying the
// peer's command list.
func (p *RunCommandPlugin) handleListReply(dev device.Sender, pkt *protocol.Packet) {
	var body struct {
		CommandList string `json:"commandList"`
	}
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		p.logger.Warn("runcommand: malformed list reply", log.Error(err))
		return
	}
	commands, err := parseCommandList(body.CommandList)
	if err != nil {
		p.logger.Warn("runcommand: undecodable list reply", log.Error(err))
		return
	}
	p.deliverCommandList(dev.ID(), commands)
}
