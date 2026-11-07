package ipc

import (
	"context"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/events"
)

// Prompt describes an interactive decision exposed to local clients.
type Prompt struct {
	PromptID  string    `json:"promptId"`
	DeviceID  string    `json:"deviceId,omitempty"`
	Kind      string    `json:"kind"`
	Summary   string    `json:"summary"`
	Options   []string  `json:"options"`
	Default   string    `json:"default"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type promptWaiter struct {
	prompt Prompt
	choice chan string
}

// PromptManager coordinates prompt events and responses without blocking packet handlers.
type PromptManager struct {
	bus     *events.Bus
	mu      sync.Mutex
	pending map[string]*promptWaiter
}

// NewPromptManager creates a prompt broker backed by the event bus.
func NewPromptManager(bus *events.Bus) *PromptManager {
	return &PromptManager{bus: bus, pending: make(map[string]*promptWaiter)}
}

// Ask publishes a prompt and waits for a response or returns its default.
func (m *PromptManager) Ask(ctx context.Context, prompt Prompt) string {
	if m.bus == nil || !m.bus.HasExplicitSubscribers(events.TypePrompt) {
		return prompt.Default
	}
	waiter := &promptWaiter{prompt: prompt, choice: make(chan string, 1)}
	m.mu.Lock()
	m.pending[prompt.PromptID] = waiter
	m.mu.Unlock()
	m.bus.Publish(events.TypePrompt, prompt.DeviceID, prompt)
	timer := time.NewTimer(time.Until(prompt.ExpiresAt))
	defer timer.Stop()
	defer func() {
		m.mu.Lock()
		delete(m.pending, prompt.PromptID)
		m.mu.Unlock()
	}()
	select {
	case choice := <-waiter.choice:
		return choice
	case <-timer.C:
		return prompt.Default
	case <-ctx.Done():
		return prompt.Default
	}
}

// Respond resolves a pending prompt and reports whether it was found.
func (m *PromptManager) Respond(promptID, choice string) bool {
	m.mu.Lock()
	waiter, ok := m.pending[promptID]
	m.mu.Unlock()
	if !ok {
		return false
	}
	valid := false
	for _, option := range waiter.prompt.Options {
		if option == choice {
			valid = true
			break
		}
	}
	if !valid {
		return false
	}
	select {
	case waiter.choice <- choice:
		return true
	default:
		return false
	}
}

// Pending returns prompts currently awaiting a response.
func (m *PromptManager) Pending() []Prompt {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Prompt, 0, len(m.pending))
	for _, waiter := range m.pending {
		result = append(result, waiter.prompt)
	}
	return result
}
