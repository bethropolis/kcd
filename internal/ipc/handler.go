package ipc

import (
	"encoding/json"
	"sort"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/plugin"
)

// Handler handles incoming IPC requests.
type Handler struct {
	devices *device.Registry
	plugins *plugin.Registry
	bus     *events.Bus
	routes  map[string]func(Request) Response
	prompts *PromptManager
}

// NewHandler creates a new IPC command handler.
func NewHandler(devices *device.Registry, plugins *plugin.Registry, bus *events.Bus) *Handler {
	h := &Handler{
		devices: devices,
		plugins: plugins,
		bus:     bus,
		routes:  make(map[string]func(Request) Response),
		prompts: NewPromptManager(bus),
	}
	h.routes[CmdPromptRespond] = h.handlePromptRespond
	return h
}

// Register adds a custom handler for a given command.
func (h *Handler) Register(command string, fn func(Request) Response) {
	h.routes[command] = fn
}

// Commands returns the registered IPC command names.
func (h *Handler) Commands() []string {
	commands := make([]string, 0, len(h.routes))
	for command := range h.routes {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	return commands
}

// HandleRequest processes an incoming IPC request and returns a response.
func (h *Handler) HandleRequest(req Request) Response {
	if fn, ok := h.routes[req.Command]; ok {
		return fn(req)
	}
	return Err(CodeUnknownCommand, "unknown command", map[string]string{"command": req.Command})
}

func (h *Handler) handlePromptRespond(req Request) Response {
	var response PromptRespondPayload
	if err := json.Unmarshal(req.Payload, &response); err != nil || response.PromptID == "" || response.Choice == "" {
		return Err(CodeInvalidPayload, "invalid prompt response", nil)
	}
	if h.prompts == nil || !h.prompts.Respond(response.PromptID, response.Choice) {
		return Err(CodeDeviceNotFound, "prompt not found", map[string]string{"promptId": response.PromptID})
	}
	return Response{OK: true}
}
